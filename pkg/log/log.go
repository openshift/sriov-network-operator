/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package log

import (
	"flag"

	zzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Options stores controller-runtime (zap) log config
var Options = &zap.Options{
	Development: true,
	// we dont log with panic level, so this essentially
	// disables stacktrace, for now, it avoids un-needed clutter in logs
	StacktraceLevel: zapcore.DPanicLevel,
	TimeEncoder:     zapcore.RFC3339NanoTimeEncoder,
	Level:           zzap.NewAtomicLevelAt(zapcore.InfoLevel),
	// log caller (file and line number) in "caller" key
	EncoderConfigOptions: []zap.EncoderConfigOption{func(ec *zapcore.EncoderConfig) { ec.CallerKey = "caller" }},
	ZapOpts:              []zzap.Option{zzap.AddCaller(), zzap.AddCallerSkip(1)},
}

// BindFlags binds controller-runtime logging flags to provided flag Set
func BindFlags(fs *flag.FlagSet) {
	Options.BindFlags(fs)
}

// InitLog initializes controller-runtime log (zap log)
// this should be called once Options have been initialized
// either by parsing flags or directly modifying Options.
func InitLog() {
	log.SetLogger(zap.New(zap.UseFlagOptions(Options)))
}

// SetLogLevel provides conversion from the operators LogLevel value ({0,1,2} where 2 is the most verbose) and sets
// the current logging level accordingly.
func SetLogLevel(operatorLevel int) {
	newLevel := operatorToZapLevel(operatorLevel)
	currLevel := Options.Level.(zzap.AtomicLevel).Level()
	if newLevel != currLevel {
		log.Log.Info("Set log verbose level", "new-level", operatorLevel, "current-level", zapToOperatorLevel(currLevel))
		Options.Level.(zzap.AtomicLevel).SetLevel(newLevel)
	}
}

func zapToOperatorLevel(zapLevel zapcore.Level) int {
	return int(zapLevel) * -1
}

func operatorToZapLevel(operatorLevel int) zapcore.Level {
	return zapcore.Level(operatorLevel * -1)
}
