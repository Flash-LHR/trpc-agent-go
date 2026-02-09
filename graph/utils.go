//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	timeType = reflect.TypeOf(time.Time{})
)

// DeepCopier defines an interface for types that can perform deep copies of themselves.
type DeepCopier interface {
	// DeepCopy performs a deep copy of the object and returns a new copy.
	DeepCopy() any
}

// deepCopyAny performs a deep copy of common JSON-serializable Go types to
// avoid sharing mutable references (maps/slices) across goroutines.
func deepCopyAny(value any) any {
	if copier, ok := value.(DeepCopier); ok {
		return copier.DeepCopy()
	}

	if out, ok := deepCopyFastPath(value); ok {
		return out
	}
	visited := make(map[uintptr]any)
	return deepCopyReflect(reflect.ValueOf(value), visited)
}

// deepCopyFastPath handles common JSON-friendly types without reflection.
func deepCopyFastPath(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case bool:
		return v, true
	case int:
		return v, true
	case int8:
		return v, true
	case int16:
		return v, true
	case int32:
		return v, true
	case int64:
		return v, true
	case uint:
		return v, true
	case uint8:
		return v, true
	case uint16:
		return v, true
	case uint32:
		return v, true
	case uint64:
		return v, true
	case uintptr:
		return v, true
	case float32:
		return v, true
	case float64:
		return v, true
	case complex64:
		return v, true
	case complex128:
		return v, true
	case string:
		return v, true
	case time.Duration:
		return v, true
	case map[string]any:
		copied := make(map[string]any, len(v))
		for k, vv := range v {
			copied[k] = deepCopyAny(vv)
		}
		return copied, true
	case map[string][]byte:
		copied := make(map[string][]byte, len(v))
		for k, b := range v {
			if b == nil {
				copied[k] = nil
				continue
			}
			out := make([]byte, len(b))
			copy(out, b)
			copied[k] = out
		}
		return copied, true
	case []any:
		copied := make([]any, len(v))
		for i := range v {
			copied[i] = deepCopyAny(v[i])
		}
		return copied, true
	case []string:
		copied := make([]string, len(v))
		copy(copied, v)
		return copied, true
	case []int:
		copied := make([]int, len(v))
		copy(copied, v)
		return copied, true
	case []float64:
		copied := make([]float64, len(v))
		copy(copied, v)
		return copied, true
	case []byte:
		copied := make([]byte, len(v))
		copy(copied, v)
		return copied, true
	case []model.Message:
		return deepCopyModelMessages(v), true
	case MessageOp:
		op, ok := deepCopyMessageOp(v)
		if !ok {
			return nil, false
		}
		return op, true
	case []MessageOp:
		out, ok := deepCopyMessageOps(v)
		if !ok {
			return nil, false
		}
		return out, true
	case time.Time:
		return v, true
	}
	return nil, false
}

func deepCopyMessageOps(in []MessageOp) ([]MessageOp, bool) {
	if in == nil {
		return nil, true
	}
	out := make([]MessageOp, len(in))
	for i, op := range in {
		if op == nil {
			continue
		}
		copied, ok := deepCopyMessageOp(op)
		if !ok {
			return nil, false
		}
		out[i] = copied
	}
	return out, true
}

func deepCopyMessageOp(op MessageOp) (MessageOp, bool) {
	switch v := op.(type) {
	case AppendMessages:
		if len(v.Items) == 0 {
			return v, true
		}
		v.Items = deepCopyModelMessages(v.Items)
		return v, true
	case ReplaceLastUser:
		return v, true
	case RemoveAllMessages:
		return v, true
	default:
		return nil, false
	}
}

func deepCopyModelMessages(in []model.Message) []model.Message {
	out := make([]model.Message, len(in))
	for i := range in {
		out[i] = in[i]
		if parts := in[i].ContentParts; len(parts) > 0 {
			out[i].ContentParts = deepCopyModelContentParts(parts)
		}
		if calls := in[i].ToolCalls; len(calls) > 0 {
			out[i].ToolCalls = deepCopyModelToolCalls(calls)
		}
	}
	return out
}

func deepCopyModelContentParts(in []model.ContentPart) []model.ContentPart {
	out := make([]model.ContentPart, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Text != nil {
			s := *in[i].Text
			out[i].Text = &s
		}
		if in[i].Image != nil {
			out[i].Image = deepCopyModelImage(in[i].Image)
		}
		if in[i].Audio != nil {
			out[i].Audio = deepCopyModelAudio(in[i].Audio)
		}
		if in[i].File != nil {
			out[i].File = deepCopyModelFile(in[i].File)
		}
	}
	return out
}

func deepCopyModelImage(in *model.Image) *model.Image {
	if in == nil {
		return nil
	}
	out := *in
	if in.Data != nil {
		out.Data = make([]byte, len(in.Data))
		copy(out.Data, in.Data)
	}
	return &out
}

func deepCopyModelAudio(in *model.Audio) *model.Audio {
	if in == nil {
		return nil
	}
	out := *in
	if in.Data != nil {
		out.Data = make([]byte, len(in.Data))
		copy(out.Data, in.Data)
	}
	return &out
}

func deepCopyModelFile(in *model.File) *model.File {
	if in == nil {
		return nil
	}
	out := *in
	if in.Data != nil {
		out.Data = make([]byte, len(in.Data))
		copy(out.Data, in.Data)
	}
	return &out
}

func deepCopyModelToolCalls(in []model.ToolCall) []model.ToolCall {
	out := make([]model.ToolCall, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Index != nil {
			idx := *in[i].Index
			out[i].Index = &idx
		}
		if args := in[i].Function.Arguments; args != nil {
			out[i].Function.Arguments = make([]byte, len(args))
			copy(out[i].Function.Arguments, args)
		}
		if extra := in[i].ExtraFields; extra != nil {
			outExtra := make(map[string]any, len(extra))
			for k, v := range extra {
				outExtra[k] = deepCopyAny(v)
			}
			out[i].ExtraFields = outExtra
		}
	}
	return out
}

// deepCopyReflect performs a deep copy using reflection with cycle detection.
func deepCopyReflect(rv reflect.Value, visited map[uintptr]any) any {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Interface:
		return copyInterface(rv, visited)
	case reflect.Ptr:
		return copyPointer(rv, visited)
	case reflect.Map:
		return copyMap(rv, visited)
	case reflect.Slice:
		return copySlice(rv, visited)
	case reflect.Array:
		return copyArray(rv, visited)
	case reflect.Struct:
		return copyStruct(rv, visited)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return reflect.Zero(rv.Type()).Interface()
	default:
		return rv.Interface()
	}
}

func copyInterface(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return nil
	}
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	return deepCopyReflect(rv.Elem(), visited)
}

func copyPointer(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return nil
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	elem := rv.Elem()
	newPtr := reflect.New(elem.Type())
	visited[ptr] = newPtr.Interface()
	newPtr.Elem().Set(reflect.ValueOf(deepCopyReflect(elem, visited)))
	return newPtr.Interface()
}

func copyMap(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return reflect.Zero(rv.Type()).Interface()
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	newMap := reflect.MakeMapWithSize(rv.Type(), rv.Len())
	visited[ptr] = newMap.Interface()
	for _, mk := range rv.MapKeys() {
		mv := rv.MapIndex(mk)
		newMap.SetMapIndex(mk,
			reflect.ValueOf(deepCopyReflect(mv, visited)))
	}
	return newMap.Interface()
}

func copySlice(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return reflect.Zero(rv.Type()).Interface()
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	l := rv.Len()
	newSlice := reflect.MakeSlice(rv.Type(), l, l)
	visited[ptr] = newSlice.Interface()
	for i := 0; i < l; i++ {
		newSlice.Index(i).Set(
			reflect.ValueOf(deepCopyReflect(rv.Index(i), visited)),
		)
	}
	return newSlice.Interface()
}

func copyArray(rv reflect.Value, visited map[uintptr]any) any {
	l := rv.Len()
	newArr := reflect.New(rv.Type()).Elem()
	for i := 0; i < l; i++ {
		elem := rv.Index(i)
		newArr.Index(i).Set(reflect.ValueOf(deepCopyReflect(elem, visited)))
	}
	return newArr.Interface()
}

func copyStruct(rv reflect.Value, visited map[uintptr]any) any {
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	if isTimeType(rv) {
		return copyTime(rv)
	}
	newStruct := reflect.New(rv.Type()).Elem()
	for i := 0; i < rv.NumField(); i++ {
		ft := rv.Type().Field(i)
		if ft.PkgPath != "" {
			continue
		}
		dstField := newStruct.Field(i)
		if !dstField.CanSet() {
			continue
		}
		srcField := rv.Field(i)
		copied := deepCopyReflect(srcField, visited)
		if copied == nil {
			dstField.Set(reflect.Zero(dstField.Type()))
			continue
		}
		srcVal := reflect.ValueOf(copied)
		if srcVal.Type().AssignableTo(dstField.Type()) {
			dstField.Set(srcVal)
		} else if srcVal.Type().ConvertibleTo(dstField.Type()) {
			dstField.Set(srcVal.Convert(dstField.Type()))
		} else {
			dstField.Set(reflect.Zero(dstField.Type()))
		}
	}
	return newStruct.Interface()
}

func isTimeType(value reflect.Value) bool {
	rt := value.Type()
	if rt == timeType {
		return true
	}

	if rt.ConvertibleTo(timeType) {
		return true
	}

	return false
}

func copyTime(value reflect.Value) any {
	if value.Type().ConvertibleTo(timeType) {
		timeVal := value.Convert(timeType).Interface()
		if t, ok := timeVal.(time.Time); ok {
			return reflect.ValueOf(t).Convert(value.Type()).Interface()
		}
	}
	return value.Interface()
}
