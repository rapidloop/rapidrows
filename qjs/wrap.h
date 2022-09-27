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

#ifndef _WRAP_H_
#define _WRAP_H_

#include <stdlib.h>		// free
#include <stdint.h>		// int64_t
#include "quickjs.h"

static inline int64_t value_tag(JSValue v) {
	return (int64_t)(JS_VALUE_GET_TAG(v));
}

static inline int64_t array_len(JSContext *ctx, JSValue v) {
	int64_t out = 0;
	JSValue lv = JS_GetPropertyStr(ctx, v, "length");
	if (!JS_IsException(lv))
		JS_ToInt64(ctx, &out, lv);
	JS_FreeValue(ctx, lv);
	return out;
}

JSValue callgo(JSContext *ctx, JSValue this_val, int argc, JSValue *argv, int magic);

static inline JSValue register_caller(JSContext *ctx, const char *name, int length, int magic) {
     return JS_NewCFunction2(ctx, (JSCFunction *)callgo, name, length, JS_CFUNC_generic_magic, magic);
}

static inline JSValue wrap_eval(JSContext *ctx, const char *input, size_t input_len) {
	return JS_Eval(ctx, input, input_len, "script", 0);
}

static inline JSValue json_stringify(JSContext *ctx, JSValueConst obj, int64_t *ok) {
	JSValue val = JS_JSONStringify(ctx, obj, JS_UNDEFINED, JS_UNDEFINED);
	*ok = (JS_IsString(val) == 1) ? 1 : 0;
	if (*ok == 0) {
		JS_FreeValue(ctx, val);
		val = JS_UNDEFINED;
	}
	return val;
}

static inline JSValue new_undefined() {
	return JS_UNDEFINED;
}

static inline JSValue json_parse(JSContext *ctx, const char *input, size_t input_len) {
	return JS_ParseJSON(ctx, input, input_len, "object");
}

static inline JSValue throw_error(JSContext *ctx, const char *msg, size_t msg_len) {
	JSValue err = JS_NewError(ctx);
	JSValue msgv = JS_NewStringLen(ctx, msg, msg_len);
	int rc = JS_SetPropertyStr(ctx, err, "message", msgv);
	if (rc == -1) { // exception occured, ignore
		JSValue ex = JS_GetException(ctx);
		JS_FreeValue(ctx, ex);
		JS_FreeValue(ctx, msgv);
		JS_FreeValue(ctx, err);
		return JS_UNDEFINED;
	} else if (rc == 0) { // failed to set property for some reason (should not happen?)
		JS_FreeValue(ctx, msgv);
		JS_FreeValue(ctx, err);
		return JS_UNDEFINED;
	}

	return JS_Throw(ctx, err); // frees err and msgv
}

#endif // _WRAP_H_
