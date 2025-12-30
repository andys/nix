package nix

import (
	"context"
	"fmt"

	"github.com/honeybadger-io/honeybadger-go"
	"github.com/rs/zerolog"
)

type LogKV map[string]any

// Stores everything needed to perform one task, eg. run one job or perform one HTTP request
type Task struct {
	context.Context
	TraceID  string // HTTP's request_id or Job's trace_id
	LogItems LogKV
	Log      zerolog.Logger
	Warnings []error
}

//
// 	// NewContext returns a new Context that carries value u.
// 	func NewContext(ctx context.Context, u *User) context.Context {
// 		return context.WithValue(ctx, userKey, u)
// 	}
//
// 	// FromContext returns the User value stored in ctx, if any.
// 	func FromContext(ctx context.Context) (*User, bool) {
// 		u, ok := ctx.Value(userKey).(*User)
// 		return u, ok
// 	}
// task.ToContext

func NewTask(ctx context.Context) *Task {
	return &Task{
		Context:  ctx,
		LogItems: LogKV{},
	}
}

func (nc *Task) ToZeroLog() *zerolog.Event {
	result := zerolog.Dict()
	for k, v := range nc.LogItems {
		result.Interface(k, v)
	}
	result = result.Array("warnings", ErrorsToZeroLog(nc.Warnings))

	return result
}

func (nc *Task) WrapErrorsAndPanics(f func() error) (reterr error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("WrapErrorsAndPanics: recover()ed")
			if err, ok := r.(error); ok {
				reterr = NewWrappedError(err) //.TakeStackTrace(debug.Stack())
			} else {
				reterr = NewWrappedError(fmt.Errorf("panic: %v", r))
			}

			// copy log context into HB context and Notify
			honeyctx := honeybadger.Context{}
			for k, v := range nc.LogItems {
				honeyctx[k] = v
			}
			_, _ = honeybadger.Notify(r, honeyctx)
		}
	}()

	reterr = f()
	if reterr != nil {
		reterr = NewWrappedError(reterr)
	}
	return reterr
}

func (nc *Task) SetRequestID(requestID string) {
	nc.AddLogContext("request_id", requestID)
	nc.SetTraceID("HTTP:" + requestID)
}

func (nc *Task) SetTraceID(traceID string) {
	nc.TraceID = traceID
}

func (nc *Task) AddWarning(err error) {
	nc.Warnings = append(nc.Warnings, err)
}

func (nc *Task) AddWarningStr(msg string) {
	nc.AddWarning(NewError(msg))
}

func (nc *Task) AddLogContext(key string, value any) {
	nc.LogItems[key] = value
}

func (nc *Task) AddToLogContextList(key, str string) {
	val := ""
	if nc.LogItems[key] != nil {
		val = nc.LogItems[key].(string) + ","
	}
	val += str

	nc.LogItems[key] = val
}
