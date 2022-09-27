/*
 * Copyright 2022 RapidLoop, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package qjs

/*
#cgo CFLAGS: -D_GNU_SOURCE
#cgo CFLAGS: -DCONFIG_BIGNUM
#cgo CFLAGS: -DDUMP_LEAKS
#cgo CFLAGS: -fno-asynchronous-unwind-tables
#cgo LDFLAGS: -lm -lpthread
#include "wrap.h"
*/
import "C"
import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"unsafe"
)

type Runtime struct {
	c *C.JSRuntime
}

func NewRuntime() *Runtime {
	return &Runtime{c: C.JS_NewRuntime()}
}

func (r *Runtime) Free() {
	C.JS_FreeRuntime(r.c)
}

func (r *Runtime) RunGC() {
	C.JS_RunGC(r.c)
}

type Context struct {
	c     *C.JSContext
	undef Value
	funcs []Function
}

var ctxMap sync.Map // map of *C.JScontext -> *Context

func (r *Runtime) NewContext() *Context {
	ctx := &Context{c: C.JS_NewContext(r.c)}
	ctx.undef = newValue(ctx, C.new_undefined())
	ctxMap.Store(uintptr(unsafe.Pointer(ctx.c)), ctx)
	return ctx
}

func (ctx *Context) Free() {
	ctxMap.Delete(uintptr(unsafe.Pointer(ctx.c)))
	C.JS_FreeContext(ctx.c)
}

func (ctx *Context) Eval(script string) (result Value, error Value) {
	cscript := C.CString(script)
	val := C.wrap_eval(ctx.c, cscript, (C.size_t)(len(script)))
	C.free(unsafe.Pointer(cscript))

	if C.JS_IsException(val) == 1 { // exception occurred
		e := C.JS_GetException(ctx.c)
		return ctx.undef, newValue(ctx, e)
	}

	return newValue(ctx, val), ctx.undef
}

func (ctx *Context) Global() Value {
	return newValue(ctx, C.JS_GetGlobalObject(ctx.c))
}

func (ctx *Context) Undefined() Value {
	return ctx.undef
}

func (ctx *Context) Object() Value {
	return newValue(ctx, C.JS_NewObject(ctx.c))
}

func (ctx *Context) Int(i int) Value {
	return newValue(ctx, C.JS_NewInt64(ctx.c, C.int64_t(i)))
}

func (ctx *Context) ObjectViaJSON(v any) (Value, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return ctx.undef, nil
	}
	js := string(j)
	cjs := C.CString(js)
	val := C.json_parse(ctx.c, cjs, (C.size_t)(len(js)))
	C.free(unsafe.Pointer(cjs))
	if C.JS_IsException(val) == 1 { // exception occurred
		return ctx.undef, ex2error(ctx)
	}
	return newValue(ctx, val), err
}

// ThrowError is used to do the equivalent of "throw new Error(msg)" from a
// Go function.
func (ctx *Context) ThrowError(msg string) Value {
	cmsg := C.CString(msg)
	val := C.throw_error(ctx.c, cmsg, (C.size_t)(len(msg)))
	C.free(unsafe.Pointer(cmsg))
	return newValue(ctx, val)
}

type Function func(ctx *Context, this Value, args []Value) Value

func (ctx *Context) NewFunction(name string, f Function) Value {
	ctx.funcs = append(ctx.funcs, f)
	idx := len(ctx.funcs) - 1
	cname := C.CString(name)
	val := C.register_caller(ctx.c, cname, C.int(len(name)), C.int(idx))
	C.free(unsafe.Pointer(cname))
	return newValue(ctx, val)
}

//export callgo
func callgo(cctx *C.JSContext, this C.JSValue, argc C.int, argv *C.JSValue, magic C.int) C.JSValue {
	// get Go context
	ctxraw, ok := ctxMap.Load(uintptr(unsafe.Pointer(cctx)))
	if !ok || ctxraw == nil {
		// panic("unknown C pointer to Go object in ctxMap")
		return C.new_undefined()
	}
	ctx, ok := ctxraw.(*Context)
	if !ok || ctx == nil {
		// panic("bad Go object in ctxMap")
		return C.new_undefined()
	}

	// get Go function to call
	if int(magic) < 0 || int(magic) >= len(ctx.funcs) {
		// panic("bad magic in callback function")
		return C.new_undefined()
	}
	f := ctx.funcs[int(magic)]

	// make args
	cargs := unsafe.Slice(argv, int(argc))
	args := make([]Value, 0, len(cargs))
	for _, arg := range cargs {
		args = append(args, newValue(ctx, arg))
	}

	// call and get result
	result := f(ctx, newValue(ctx, this), args)
	return result.c
}

type Value struct {
	ctx *Context
	c   C.JSValue
}

func newValue(ctx *Context, c C.JSValue) Value {
	return Value{ctx: ctx, c: c}
}

func (v Value) Free() {
	C.JS_FreeValue(v.ctx.c, v.c)
}

func (v Value) Dup() Value {
	return Value{ctx: v.ctx, c: C.JS_DupValue(v.ctx.c, v.c)}
}

func (v Value) Any() any {
	return val2any(v.ctx, v.c)
}

func (v Value) SetProperty(prop string, value Value) error {
	cprop := C.CString(prop)
	rc := int(C.JS_SetPropertyStr(v.ctx.c, v.c, cprop, value.c))
	C.free(unsafe.Pointer(cprop))
	if rc == -1 { // exception occurred
		return ex2error(v.ctx)
	}
	return nil
}

func (v Value) GetProperty(prop string) Value {
	cprop := C.CString(prop)
	v2 := newValue(v.ctx, C.JS_GetPropertyStr(v.ctx.c, v.c, cprop))
	C.free(unsafe.Pointer(cprop))
	return v2
}

func (v Value) GetIndex(idx uint32) Value {
	return newValue(v.ctx, C.JS_GetPropertyUint32(v.ctx.c, v.c, (C.uint32_t)(idx)))
}

func (v Value) Tag() int {
	return int(C.value_tag(v.c))
}

func (v Value) Unmarshal(obj any) error {
	if v.Tag() != TagObject {
		return errors.New("not an object")
	}
	var ok C.int64_t
	j := C.json_stringify(v.ctx.c, v.c, &ok)
	if ok == 0 {
		return errors.New("json stringify failed")
	}
	js := val2str(v.ctx, j)
	C.JS_FreeValue(v.ctx.c, j)
	return json.Unmarshal([]byte(js), obj)
}

const (
	TagBigDecimal    = -11 // not enabled, qjs extension
	TagBigInt        = -10
	TagBigFloat      = -9 // not enabled, qjs extension
	TagSymbol        = -8
	TagString        = -7
	TagObject        = -1
	TagInt           = 0
	TagBool          = 1
	TagNull          = 2
	TagUndefined     = 3
	TagUninitialized = 4
	TagCatchOffset   = 5
	TagException     = 6
	TagFloat64       = 7
)

func val2any(ctx *Context, c C.JSValue) any {
	switch tag := int(C.value_tag(c)); tag {
	case TagInt:
		var val C.int64_t
		C.JS_ToInt64(ctx.c, &val, c)
		return int64(val)
	case TagBool:
		val := C.JS_ToBool(ctx.c, c)
		return val == 1
	case TagFloat64:
		var val C.double
		C.JS_ToFloat64(ctx.c, &val, c)
		return float64(val)
	case TagObject:
		if C.JS_IsError(ctx.c, c) == 1 { // js Error class
			// the error class has a toString method that returns the message
			return &Error{Message: val2str(ctx, c)}
		} else if C.JS_IsArray(ctx.c, c) == 1 { // js Array class
			n := int(C.array_len(ctx.c, c))
			val := make([]any, n)
			for i := 0; i < n; i++ {
				elem := C.JS_GetPropertyUint32(ctx.c, c, (C.uint32_t)(i))
				val[i] = val2any(ctx, elem)
				C.JS_FreeValue(ctx.c, elem)
			}
			return val
		} else {
			var ok C.int64_t
			j := C.json_stringify(ctx.c, c, &ok)
			if ok == 0 {
				return errors.New("json stringify failed")
			}
			js := val2str(ctx, j)
			C.JS_FreeValue(ctx.c, j)
			var val map[string]any
			if err := json.Unmarshal([]byte(js), &val); err != nil {
				return fmt.Errorf("json unmarshal failed: %v", err)
			}
			return val
		}
	case TagBigInt:
		val, ok := new(big.Int).SetString(val2str(ctx, c), 10)
		if !ok {
			return errors.New("invalid BigInt string") // should not happen
		}
		if val.IsInt64() {
			return val.Int64()
		}
		return val
	case TagString:
		return val2str(ctx, c)
	case TagSymbol:
		// symbol class has a toString method
		return val2str(ctx, c)
	}
	// null, undefined, uninitialized => nil
	return nil
}

func val2str(ctx *Context, v C.JSValue) string {
	p := C.JS_ToCString(ctx.c, v)
	val := C.GoString(p)
	C.JS_FreeCString(ctx.c, p)
	return val
}

// note: frees the qjs exception object!
func ex2error(ctx *Context) error {
	e := C.JS_GetException(ctx.c)
	msg := val2str(ctx, e)
	C.JS_FreeValue(ctx.c, e)
	return &Error{Message: msg}
}

// Error is used only when a Javascript error object has to be returned,
// usually because it was thrown as an exception.
type Error struct {
	Message string
}

func (e Error) Error() string {
	return e.Message
}
