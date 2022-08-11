package logger

import (
	"compress/gzip"
	"fmt"
	rotate "github.com/lestrrat-go/file-rotatelogs"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLogFileFmt = "log/%s.log"
	splitFmt="-%s"
	defaultRotateOptionSuffix=".%Y%m%d"
	fileLineNumberFmt="%s:%s"
	compressFileFmt="%s.gz"
)

var (
	reg=regexp.MustCompile(fmt.Sprintf("log\\.([^ .=]+)(?:\\.(trace|debug|info|warn|warning|error|fatal|panic))?\\.(%s|%s|%s|%s|%s)(?:\\.option\\.([^ =]+))?=(.+)",logOptionName,logOptionLevel,logOptionFormat,logOptionRotate,logOptionSplit))
	configs=make(map[string]*logConfig)
	loggers=make(map[string]*logrus.Logger)
	defaultLogger=logrus.New()
)

func init(){
	if content, err := os.ReadFile("log.properties");err==nil{
		parseConfigs(content)
	}else{
		panic(err)
	}
	createLoggers()
	createDefaultLogger()
}

func Switch(name string) *logrus.Logger{
	if logger,OK:=loggers[name];OK{
		return logger
	}
	return defaultLogger
}

func createDefaultLogger(){
	defaultLogger.SetReportCaller(true)
	defaultLogger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat:  defaultFormatOptionTimeFormat,
		CallerPrettyfier: newCallerPrettifier().Pretty,
	})
}
func createLoggers(){
	for _,cfg:=range configs{
		if cfg.global.Suffix==""{
			cfg.global.Suffix=defaultRotateOptionSuffix
		}
		formatters:= newMultiFormatter(cfg.global.GetFormatter())
		writers:=make(lfshook.WriterMap)
		logger:=&logrus.Logger{
			Out:          os.Stderr,
			Formatter:    formatters,
			Hooks:        make(logrus.LevelHooks),
			Level:        cfg.Level,
			ExitFunc:     os.Exit,
			ReportCaller: true,
		}
		writer:=cfg.global.NewWriter()
		for _,level:=range cfg.Splits {
			if !logger.IsLevelEnabled(level) {
				continue
			}
			c := cfg.configs[level]
			c.MergeRotateOption(cfg.global.rotateOptions, cfg.global.Suffix)
			c.MergeFormatOption(cfg.global)
			writers[level] = c.NewWriter()
			formatters.AddFormatter(level, c.GetFormatter())
		}
		for _,level:=range logrus.AllLevels{
			if !logger.IsLevelEnabled(level)||writers[level]!=nil{
				continue
			}
			writers[level]=writer
			if c,OK:=cfg.configs[level];OK{
				c.MergeRotateOption(cfg.global.rotateOptions,cfg.global.Suffix)
				c.MergeFormatOption(cfg.global)
				formatters.AddFormatter(level,c.GetFormatter())
			}
		}
		logger.AddHook(lfshook.NewHook(writers,formatters))
		loggers[cfg.Name]=logger
  	}
}

func parseConfigs(contents []byte) {
	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		texts := strings.Split(line, "#")
		if line = strings.TrimSpace(texts[0]);line == "" {
			continue
		}
		res := reg.FindStringSubmatch(line)
		if len(res) == 0 {
			continue
		}
		name:=strings.ToLower(res[1])
		cfg,OK:=configs[name]
		if !OK{
			cfg= newLogConfig(name)
			configs[name]=cfg
		}
		cfg.ParseConfig(res[2:])
	}
	return
}


const (
	logOptionName="name"
	logOptionSplit="split"
	logOptionLevel="level"
	logOptionFormat="format"
	logOptionRotate="rotate"

	defaultLogOptionFormatType=logOptionFormatTypeJson
	logOptionFormatTypeJson="json"
	logOptionFormatTypeText="text"
	logOptionSplitAll="all"
)

type logConfig struct {
	Name string
	Splits []logrus.Level
	Level logrus.Level
	global *configItem
	configs map[logrus.Level]*configItem
}

func newLogConfig(name string) *logConfig{
	return &logConfig{Name: name,Level:logrus.InfoLevel,global: newConfigItem(fmt.Sprintf(defaultLogFileFmt,name)),configs: make(map[logrus.Level]*configItem)}
}

func (l *logConfig)ParseConfig(values []string){
	if values[0]==""{
		switch strings.ToLower(values[1]){
		case logOptionLevel:
			if level,err:=logrus.ParseLevel(values[3]);err==nil{
				l.Level=level
			}
		case logOptionName:
			oldName:=l.global.FileName
			l.global.ParseItem(values[1:])
			for level,cfg:=range l.configs{
				if cfg.FileName==l.genFileNameWithLevel(oldName,level){
					cfg.FileName=l.genFileNameWithLevel(l.global.FileName,level)
				}
			}
		case logOptionSplit:
			levels:=strings.Split(strings.ToLower(values[3]),",")
			for _,levelStr:=range levels{
				if levelStr==logOptionSplitAll{
					l.Splits=logrus.AllLevels
					for _,level:=range logrus.AllLevels{
						if l.configs[level]==nil{
							l.configs[level]= newConfigItem(l.genFileNameWithLevel(l.global.FileName,level))
						}
					}
					break
				}
				if level,err:=logrus.ParseLevel(levelStr);err==nil{
					l.Splits=append(l.Splits,level)
					if l.configs[level]==nil{
						l.configs[level]= newConfigItem(l.genFileNameWithLevel(l.global.FileName,level))
					}
				}
			}
		default:
			l.global.ParseItem(values[1:])
		}
	}else if level,err:=logrus.ParseLevel(values[0]);err==nil{
		cfg,OK:=l.configs[level]
		if !OK{
			cfg= newConfigItem(l.genFileNameWithLevel(l.global.FileName,level))
			l.configs[level]=cfg
		}
		cfg.ParseItem(values[1:])
	}
}

func (l *logConfig)genFileNameWithLevel(fileName string,level logrus.Level) string{
	return addSuffix(fileName,fmt.Sprintf(splitFmt,level))
}

const (
	formatOptionTimeFormat ="timeformat"
	formatOptionDisableTimestamp="disabletimestamp"
	formatOptionDisableHtmlEscape="disablehtmlescape"
	formatOptionDataKey="datakey"
	formatOptionPretty="pretty"
	formatOptionForceColors="forcecolors"
	formatOptionDisableColors="disablecolors"
	formatOptionForceQuote="forcequote"
	formatOptionDisableQuote="disablequote"
	formatOptionOverrideColors="overridecolors"
	formatOptionFullTimestamp="fulltimestamp"
	formatOptionDisableSorting="disablesorting"
	formatOptionDisableLevelTruncation="disableleveltruncation"
	formatOptionPadLevelText="padleveltext"
	formatOptionQuoteEmptyFields ="quoteemptyfields"
	formatOptionReportCaller="reportcaller"
	formatOptionShortFileDisable="shortfiledisable"
	formatOptionShortFileRootPath="shortfileroot"
	formatOptionLineNumberDisable="linenumberdisable"
	formatOptionFunctionDisable="functiondisable"
	formatOptionFieldMap="fieldmap"

	defaultFormatOptionShortFileRootPath="{base}"
	defaultFormatOptionTimeFormat="2006-01-02 15:04:05"

)

const (
	rotateOptionMaxAge="maxage"
	rotateOptionCount="count"
	rotateOptionForceNewFile="forceNewFile"
	rotateOptionDuration="duration"
	rotationOptionSize="size"
	rotationOptionSuffix="suffix"
	rotationOptionLocation="location"
	rotationOptionCompress="compress"
)

type configItem struct {
	FileName string
	FormatType string
	Suffix string
	prettifier *callerPrettifier
	json *logrus.JSONFormatter
	text *logrus.TextFormatter
	fieldMap logrus.FieldMap
	rotateOptions map[string]rotate.Option
}

func newConfigItem(defaultFileName string) *configItem{
	return &configItem{
		FileName:      defaultFileName,
		FormatType:    defaultLogOptionFormatType,
		prettifier:    newCallerPrettifier(),
		json:          &logrus.JSONFormatter{},
		text:          &logrus.TextFormatter{},
		fieldMap:      make(logrus.FieldMap),
		rotateOptions: make(map[string]rotate.Option),
	}
}

func (c *configItem)ParseItem(values []string){
	switch strings.ToLower(values[0]) {
	case logOptionName:
		c.FileName=values[2]
	case logOptionFormat:
		if values[1]==""{
			if strings.ToLower(values[2])==logOptionFormatTypeText{
				c.FormatType=logOptionFormatTypeText
			}
		}else{
			c.parseFormatOption(values)
		}
	case logOptionRotate:
		c.parseRotateOption(values)
	}

}

func (c *configItem)MergeFormatOption(cfg *configItem){
	jsonFormatter:=cfg.json
	textFormatter:=cfg.text
	fieldMap:=cfg.fieldMap
	shortFileRoot:=cfg.prettifier.shortFileRoot
	//json
	if !c.json.DisableHTMLEscape{
		c.json.DisableHTMLEscape=jsonFormatter.DisableHTMLEscape
	}
	if !c.json.DisableTimestamp{
		c.json.DisableTimestamp=jsonFormatter.DisableTimestamp
	}
	if c.json.TimestampFormat==""{
		c.json.TimestampFormat=jsonFormatter.TimestampFormat
	}
	if !c.json.PrettyPrint{
		c.json.PrettyPrint=jsonFormatter.PrettyPrint
	}
	//text
	if !c.text.DisableTimestamp{
		c.text.DisableTimestamp=textFormatter.DisableTimestamp
	}
	if !c.text.PadLevelText{
		c.text.PadLevelText=textFormatter.PadLevelText
	}
	if !c.text.EnvironmentOverrideColors{
		c.text.EnvironmentOverrideColors=textFormatter.EnvironmentOverrideColors
	}
	if !c.text.DisableQuote{
		c.text.DisableQuote=textFormatter.DisableQuote
	}
	if !c.text.ForceQuote{
		c.text.ForceQuote=textFormatter.ForceQuote
	}
	if !c.text.ForceColors{
		c.text.ForceColors=textFormatter.ForceColors
	}
	if !c.text.DisableColors{
		c.text.DisableColors=textFormatter.DisableColors
	}
	if !c.text.DisableLevelTruncation{
		c.text.DisableLevelTruncation=textFormatter.DisableLevelTruncation
	}
	if !c.text.DisableSorting{
		c.text.DisableSorting=textFormatter.DisableSorting
	}
	if !c.text.FullTimestamp{
		c.text.FullTimestamp=textFormatter.FullTimestamp
	}
	if !c.text.QuoteEmptyFields{
		c.text.QuoteEmptyFields=textFormatter.QuoteEmptyFields
	}

	if c.text.TimestampFormat==""{
		c.text.TimestampFormat=textFormatter.TimestampFormat
	}
	//fieldMap
	for k,v:=range fieldMap{
		if _,OK:=c.fieldMap[k];!OK{
			c.fieldMap[k]=v
		}
	}
	//callerPrettifier
	if !c.prettifier.shortFileDisable&&c.prettifier.shortFileRoot==""&&shortFileRoot!=""{
		c.prettifier.shortFileRoot=shortFileRoot
	}
}

func(c *configItem)MergeRotateOption(options map[string]rotate.Option,suffix string){
	for k,option:=range options{
		if k==rotateOptionMaxAge||k==rotateOptionCount{
			continue
		}
		if _,OK:=c.rotateOptions[k];!OK{
			c.rotateOptions[k]=option
		}
	}
	if c.Suffix==""{
		c.Suffix=suffix
	}
	_,mOK:=c.rotateOptions[rotateOptionMaxAge]
	_,cOK:=c.rotateOptions[rotateOptionCount]
	mOption,pmOK:=c.rotateOptions[rotateOptionMaxAge]
	cOption,pcOK:=c.rotateOptions[rotateOptionCount]
	if !mOK&&!cOK{
		if pmOK{
			c.rotateOptions[rotateOptionMaxAge]=mOption
		}else if pcOK{
			c.rotateOptions[rotateOptionCount]=cOption
		}
	}
}

func (c *configItem)GetFormatter()(formatter logrus.Formatter){
	if len(c.fieldMap)>0{
		c.json.FieldMap=c.fieldMap
		c.text.FieldMap=c.fieldMap
	}
	if c.prettifier.shortFileRoot==defaultFormatOptionShortFileRootPath{
		c.prettifier.shortFileRoot=""
	}
	c.json.CallerPrettyfier=c.prettifier.Pretty
	c.text.CallerPrettyfier=c.prettifier.Pretty
	if c.FormatType==logOptionFormatTypeJson{
		formatter=c.json
	}else{
		formatter= c.text
	}
	return
}

func (c *configItem)NewWriter() *rotate.RotateLogs{
	writer,err:=rotate.New(c.rotateFileName(),c.getRotateOptions()...)
	if err!=nil{
		panic(err)
	}
	return writer
}

func (c *configItem)parseFormatOption(values []string){
	switch strings.ToLower(values[1]) {
	case formatOptionTimeFormat:
		c.json.TimestampFormat=values[2]
		c.text.TimestampFormat=values[2]
	case formatOptionDisableTimestamp:
		c.json.DisableTimestamp=trueValue(values[2])
		c.text.DisableTimestamp=trueValue(values[2])
	case formatOptionDisableHtmlEscape:
		c.json.DisableHTMLEscape=trueValue(values[2])
	case formatOptionDataKey:
		c.json.DataKey=values[2]
	case formatOptionPretty:
		c.json.PrettyPrint=trueValue(values[2])
	case formatOptionForceColors:
		c.text.ForceColors=trueValue(values[2])
	case formatOptionDisableColors:
		c.text.DisableColors=trueValue(values[2])
	case formatOptionForceQuote:
		c.text.ForceQuote=trueValue(values[2])
	case formatOptionDisableQuote:
		c.text.DisableQuote=trueValue(values[2])
	case formatOptionOverrideColors:
		c.text.EnvironmentOverrideColors=trueValue(values[2])
	case formatOptionFullTimestamp:
		c.text.FullTimestamp=trueValue(values[2])
	case formatOptionDisableSorting:
		c.text.DisableSorting=trueValue(values[2])
	case formatOptionDisableLevelTruncation:
		c.text.DisableLevelTruncation=trueValue(values[2])
	case formatOptionPadLevelText:
		c.text.PadLevelText=trueValue(values[2])
	case formatOptionQuoteEmptyFields:
		c.text.QuoteEmptyFields=trueValue(values[2])
	case formatOptionReportCaller:
		c.prettifier.reportCaller=trueValue(values[2])
	case formatOptionShortFileDisable:
		c.prettifier.shortFileDisable=trueValue(values[2])
	case formatOptionShortFileRootPath:
		c.prettifier.shortFileRoot=values[2]
	case formatOptionLineNumberDisable:
		c.prettifier.lineNumberDisable=trueValue(values[2])
	case formatOptionFunctionDisable:
		c.prettifier.functionDisable=trueValue(values[2])
	default:
		if strs:=strings.SplitN(strings.ToLower(values[1]),".",2);strs[0]==formatOptionFieldMap{
			c.addFieldMap(strs[1],values[2])

		}
	}
}


func(c *configItem)parseRotateOption(values []string){
	switch strings.ToLower(values[1]) {
	case rotationOptionSuffix:
		c.Suffix=values[2]
	case rotateOptionMaxAge:
		if age,err:=time.ParseDuration(values[2]);err==nil&&age>0{
			c.rotateOptions[rotateOptionMaxAge]=rotate.WithMaxAge(age)
			delete(c.rotateOptions,rotateOptionCount)
		}
	case rotateOptionCount:
		if count,err:=strconv.ParseUint(values[2],10,32);err==nil&&count>0{
			c.rotateOptions[rotateOptionCount]=rotate.WithRotationCount(uint(count))
			delete(c.rotateOptions,rotateOptionMaxAge)
		}
	case rotateOptionDuration:
		if dur,err:=time.ParseDuration(values[2]);err==nil{
			c.rotateOptions[rotateOptionDuration]=rotate.WithRotationTime(dur)
		}
	case rotateOptionForceNewFile:
		if trueValue(values[2]){
			c.rotateOptions[rotateOptionForceNewFile]=rotate.ForceNewFile()
		}
	case rotationOptionLocation:
		if location,err:=time.LoadLocation(values[2]);err==nil{
			c.rotateOptions[rotationOptionLocation]=rotate.WithLocation(location)
		}
	case rotationOptionSize:
		if size,err:=strconv.ParseInt(values[2],10,64);err==nil{
			c.rotateOptions[rotationOptionSize]=rotate.WithRotationSize(size)
		}
	case rotationOptionCompress:
		if trueValue(values[2]){
			//TODO later
			//c.rotateOptions[rotationOptionCompress]=rotate.WithHandler(compressor)
		}
	default:

	}
}

func (c *configItem)addFieldMap(key,value string){
	switch key {
	case logrus.FieldKeyMsg:
		c.fieldMap[logrus.FieldKeyMsg]=value
	case logrus.FieldKeyLevel:
		c.fieldMap[logrus.FieldKeyLevel]=value
	case logrus.FieldKeyTime:
		c.fieldMap[logrus.FieldKeyTime]=value
	case logrus.FieldKeyLogrusError:
		c.fieldMap[logrus.FieldKeyLogrusError]=value
	case logrus.FieldKeyFunc:
		c.fieldMap[logrus.FieldKeyFunc]=value
	case logrus.FieldKeyFile:
		c.fieldMap[logrus.FieldKeyFile]=value
	default:
	}
}

func(c *configItem)getRotateOptions()[]rotate.Option{
	options:=make([]rotate.Option,0,len(c.rotateOptions)+1)
	for _,option:=range c.rotateOptions{
		options=append(options,option)
	}
	options=append(options,rotate.WithLinkName(c.FileName))
	return options
}

func(c *configItem)rotateFileName()string{
	return addSuffix(c.FileName,c.Suffix)
}

type multiFormatter struct{
	defaultFormatters  logrus.Formatter
	formatters map[logrus.Level]logrus.Formatter
}

func newMultiFormatter(defaultFormatters logrus.Formatter) *multiFormatter{
	return &multiFormatter{defaultFormatters: defaultFormatters,formatters: make(map[logrus.Level]logrus.Formatter)}
}

func (m *multiFormatter)Format(entry *logrus.Entry) ([]byte, error){
	formatter,OK:=m.formatters[entry.Level]
	if !OK{
		formatter=m.defaultFormatters
	}
	return formatter.Format(entry)
}

func (m *multiFormatter)AddFormatter(level logrus.Level,formatter logrus.Formatter){
	m.formatters[level]=formatter
}

var compressor=&compressHandler{}

type compressHandler struct{}

func (h *compressHandler)Handle(event rotate.Event){
	if event.Type()==rotate.FileRotatedEventType{
		e:=event.(*rotate.FileRotatedEvent)
		compress(e.PreviousFile())
	}
}

func compress(filename string){
	if filename==""{
		return
	}
	compressed:=fmt.Sprintf(compressFileFmt,filename)
	if newFile, err := os.Create(compressed);err==nil{
		defer newFile.Close()
		if file, err := os.Open(filename);err==nil{
			defer file.Close()
			if fileStat, err := file.Stat();err==nil{
				zw := gzip.NewWriter(newFile)
				zw.Name = fileStat.Name()
				zw.ModTime = fileStat.ModTime()
				if _, err = io.Copy(zw, file);err==nil{
					if err=zw.Flush();err==nil{
						_=os.Remove(filename)
					}
				}
			}
		}
	}
}

func trueValue(v string)(b bool){
	b,_=strconv.ParseBool(v)
	return
}

func addSuffix(ori,suffix string) string{
	dir,fName:=filepath.Split(ori)
	ext:=filepath.Ext(fName)
	return fmt.Sprintf("%s%s%s%s",dir,fName[:len(fName)-len(ext)],suffix,ext)
}

type callerPrettifier struct{
	reportCaller bool
	shortFileDisable bool
	lineNumberDisable bool
	functionDisable bool
	shortFileRoot string
}

func newCallerPrettifier() *callerPrettifier{
	return &callerPrettifier{reportCaller:true}
}

func (c *callerPrettifier)Pretty(frame *runtime.Frame) (function string, file string){
	if !c.reportCaller{
		return
	}
	if !c.functionDisable{
		function=frame.Function
	}

	if c.shortFileDisable{
		file=frame.File
	}else if c.shortFileRoot!=""{
		paths:=strings.SplitN(frame.File,c.shortFileRoot,2)
		if len(paths)>1{
			file=strings.TrimPrefix(paths[1],string(filepath.Separator))
		}
	}
	if file==""{
		file=filepath.Base(frame.File)
	}
	if !c.lineNumberDisable{
		file=fmt.Sprintf(fileLineNumberFmt,file,strconv.Itoa(frame.Line))
	}
	return
}