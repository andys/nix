package nix

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
)

func PanicOnErrValExceptNoRows[T any](val T, err error) T {
	if err == pgx.ErrNoRows {
		return val
	}
	return PanicOnErrVal(val, err)
}

func PanicOnErrVal[T any](val T, err error) T {
	if err != nil {
		panic(NewWrappedError(err))
	}
	return val
}

func PanicOnErr(err error) {
	if err != nil {
		newerr := NewWrappedError(err)
		panic(newerr)
	}
}

func WarnOnErr(err error) {
	if err != nil {
		_ = NewWrappedError(err).AddFlag(WarningFlag)
	}
}

func WarnOnErrVal[T any](val T, err error) T {
	if err != nil {
		_ = NewWrappedError(err).AddFlag(WarningFlag)
	}
	return val
}

func ErrorsToZeroLog(errs []error) *zerolog.Array {
	result := zerolog.Arr()
	for _, err := range errs {
		result = result.Dict(ErrorToZeroLog(err))
	}
	return result
}

func ErrorToZeroLog(err error) *zerolog.Event {
	result := zerolog.Dict()
	if err == nil {
		return result
	}
	nixError, isNixError := err.(*Error)
	if isNixError {
		result.Err(nixError.WrappedError)
		if nixError.HttpCode > 0 {
			result.Int("http_code", nixError.HttpCode)
		}
		result.Strs("stack_trace", nixError.StackTrace)
		result.Strs("user_errmsgs", nixError.UserMessages)
		errlogctxs := zerolog.Dict()
		for k, v := range nixError.ErrorLogContext {
			errlogctxs.Interface(k, v)
		}
		result.Dict("error_context", errlogctxs)
	} else {
		result.Interface("error", err)
	}

	return result
}

type ErrorFlags int

const (
	WarningFlag ErrorFlags = 1 << iota
)

type Error struct {
	HttpCode        int
	UserMessages    []string
	WrappedError    error
	ErrorLogContext LogKV
	StackTrace      []string
	Flags           ErrorFlags
}

func (ae *Error) IsNil() bool {
	return ae == nil
}

func (ae *Error) Error() string {
	if ae.WrappedError != nil {
		return ae.WrappedError.Error() // chain the error interface
	} else {
		return ""
	}
}

func (ae *Error) AddFlag(f ErrorFlags) *Error {
	ae.Flags &= f
	return ae
}

func (ae *Error) HasFlag(f ErrorFlags) bool {
	return ae.Flags&f > 0
}

func (ae *Error) Wrap(err error) *Error {
	if err == nil {
		panic("Can't wrap nil!")
	}
	if err == ae.WrappedError {
		panic("Can't wrap myself!")
	}
	_, isNixError := err.(*Error)
	if isNixError {
		panic("Can't wrap an NixError in another NixError!")
	}

	ae.WrappedError = err
	return ae
}

func (ae *Error) HttpError(code int) *Error {
	ae.HttpCode = code
	return ae.AddMessage(fmt.Sprintf("%d - %s", code, http.StatusText(code)))
}

func (ae *Error) AddMessage(msg string) *Error {
	ae.UserMessages = append(ae.UserMessages, msg)
	return ae
}

func (ae *Error) AddLogContext(key string, val any) *Error {
	ae.ErrorLogContext[key] = val
	return ae
}

func NewWrappedErrorWithoutTrace(err error) *Error {
	nixError, isNixError := err.(*Error)

	if isNixError {
		return nixError
	}

	ae := &Error{UserMessages: []string{}, StackTrace: []string{}, ErrorLogContext: LogKV{}}
	if err == pgx.ErrNoRows {
		ae.HttpCode = http.StatusNotFound
		ae.UserMessages = append(ae.UserMessages, "404 - Not found")
	}

	return ae.Wrap(err)
}

func NewWrappedError(err error) *Error {
	return NewWrappedErrorWithoutTrace(err).WithStackTrace()
}

func NewHttpError(code int) *Error {
	return NewError(http.StatusText(code)).HttpError(code)
}

func NewError(errorString string) *Error {
	return NewWrappedError(errors.New(errorString))
}

func (ae *Error) WithStackTrace() *Error {
	return ae.TakeStackTrace(nil)
}

func (ae *Error) TakeStackTrace(stackTrace []byte) *Error {
	var s []byte
	if stackTrace == nil {
		s = make([]byte, 64000)
		runtime.Stack(s, false)
	} else {
		s = stackTrace
	}

	scanner := bufio.NewScanner(bytes.NewReader(s))

	if !scanner.Scan() {
		return ae
	}
	if !strings.HasPrefix(scanner.Text(), "goroutine ") {
		return ae
	}
	for i := 0; i < 4; i++ { // skip first lines
		scanner.Scan()
	}

	for scanner.Scan() {
		funcname := scanner.Text()
		parensIndex := strings.IndexByte(funcname, '(')
		if parensIndex < 0 {
			break
		}
		line := funcname[:parensIndex] + " @ "

		if !scanner.Scan() {
			break
		}
		path := scanner.Text()
		fields := strings.Fields(path)
		if len(fields) < 1 {
			break
		}
		if strings.Contains(fields[0], "/vendor/") || strings.Contains(fields[0], "/nix/") {
			continue
		}

		line += fields[0]
		ae.StackTrace = append(ae.StackTrace, line)
	}

	return ae
}
