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
	"fmt"
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
	if out, ok := deepCopyPrimitiveFastPath(value); ok {
		return out, true
	}
	switch v := value.(type) {
	case map[string]any:
		return deepCopyMapStringAny(v), true
	case map[string][]byte:
		return deepCopyMapStringBytes(v), true
	case []any:
		return deepCopySliceAny(v), true
	case []string:
		return cloneSlice(v), true
	case []int:
		return cloneSlice(v), true
	case []float64:
		return cloneSlice(v), true
	case []byte:
		return cloneSlice(v), true
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

func deepCopyPrimitiveFastPath(value any) (any, bool) {
	if out, ok := deepCopyNumericFastPath(value); ok {
		return out, true
	}
	switch v := value.(type) {
	case nil:
		return nil, true
	case bool:
		return v, true
	case string:
		return v, true
	case time.Duration:
		return v, true
	case time.Time:
		return v, true
	default:
		return nil, false
	}
}

func deepCopyNumericFastPath(value any) (any, bool) {
	switch v := value.(type) {
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
	default:
		return nil, false
	}
}

func deepCopyMapStringAny(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	copied := make(map[string]any, len(in))
	for k, v := range in {
		copied[k] = deepCopyAny(v)
	}
	return copied
}

func deepCopyMapStringBytes(in map[string][]byte) map[string][]byte {
	if in == nil {
		return nil
	}
	copied := make(map[string][]byte, len(in))
	for k, v := range in {
		copied[k] = cloneSlice(v)
	}
	return copied
}

func deepCopySliceAny(in []any) []any {
	if in == nil {
		return nil
	}
	copied := make([]any, len(in))
	for i := range in {
		copied[i] = deepCopyAny(in[i])
	}
	return copied
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
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
		if v.Items != nil {
			v.Items = deepCopyModelMessages(v.Items)
		}
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
	if in == nil {
		return nil
	}
	out := make([]model.Message, len(in))
	for i := range in {
		out[i] = in[i]
		if parts := in[i].ContentParts; parts != nil {
			out[i].ContentParts = deepCopyModelContentParts(parts)
		}
		if calls := in[i].ToolCalls; calls != nil {
			out[i].ToolCalls = deepCopyModelToolCalls(calls)
		}
	}
	return out
}

func deepCopyModelContentParts(in []model.ContentPart) []model.ContentPart {
	if in == nil {
		return nil
	}
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
	if in == nil {
		return nil
	}
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
	if isTimeType(rv.Type()) {
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

func isTimeType(rt reflect.Type) bool {
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

// isJSONUnsafeKind reports whether a reflect.Kind cannot be handled
// by encoding/json (chan, func, unsafe pointer).
func isJSONUnsafeKind(k reflect.Kind) bool {
	switch k {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func deepCopyByInterface(value any) (any, bool) {
	if copier, ok := value.(DeepCopier); ok {
		return copier.DeepCopy(), true
	}
	return nil, false
}

func deepCopyByReflectValue(value reflect.Value) (any, bool) {
	if !value.IsValid() || !value.CanInterface() {
		return nil, false
	}
	if copier, ok := value.Interface().(DeepCopier); ok {
		return copier.DeepCopy(), true
	}
	return nil, false
}

func valueIsJSONUnsafe(value any) bool {
	if value == nil {
		return false
	}
	return mapValueIsJSONUnsafe(reflect.ValueOf(value))
}

// hasJSONUnsafeField returns true when a struct type contains exported
// fields that encoding/json cannot serialize.
func hasJSONUnsafeField(rt reflect.Type) bool {
	visiting := make(map[reflect.Type]bool)
	return hasJSONUnsafeType(rt, visiting)
}

func hasJSONUnsafeType(rt reflect.Type, visiting map[reflect.Type]bool) bool {
	for rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if isJSONUnsafeKind(rt.Kind()) {
		return true
	}
	if isTimeType(rt) {
		return false
	}

	switch rt.Kind() {
	case reflect.Struct:
		if visiting[rt] {
			return false
		}
		visiting[rt] = true
		defer delete(visiting, rt)

		for i := 0; i < rt.NumField(); i++ {
			ft := rt.Field(i)
			if shouldSkipJSONField(ft) {
				continue
			}
			if hasJSONUnsafeType(ft.Type, visiting) {
				return true
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		return hasJSONUnsafeType(rt.Elem(), visiting)
	case reflect.Map:
		if hasJSONUnsafeType(rt.Key(), visiting) {
			return true
		}
		return hasJSONUnsafeType(rt.Elem(), visiting)
	case reflect.Interface:
		return false
	default:
		return false
	}
}

// jsonSafeCopy produces a deep copy of value that is safe for
// encoding/json.Marshal. Structs containing chan/func fields are
// converted to map[string]any with those fields omitted.
func jsonSafeCopy(value any) any {
	visited := make(map[uintptr]any)
	return jsonSafeCopyWithVisited(value, visited)
}

func jsonSafeCopyWithVisited(value any, visited map[uintptr]any) any {
	if value == nil {
		return nil
	}
	if out, ok := deepCopyByInterface(value); ok {
		return out
	}
	if out, ok := jsonSafeFastPath(value, visited); ok {
		return out
	}
	return jsonSafeReflect(reflect.ValueOf(value), visited)
}

// jsonSafeFastPath handles common JSON-friendly types without
// reflection, delegating nested values to jsonSafeCopyWithVisited.
// For maps, unsafe values are dropped to match jsonSafeCopyMap behavior.
func jsonSafeFastPath(value any, visited map[uintptr]any) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		ptr := reflect.ValueOf(v).Pointer()
		if cached, ok := visited[ptr]; ok {
			return cached, true
		}
		copied := make(map[string]any, len(v))
		visited[ptr] = copied
		for k, vv := range v {
			copiedVal := jsonSafeCopyWithVisited(vv, visited)
			if copiedVal == nil && valueIsJSONUnsafe(vv) {
				continue // Skip non-serializable values.
			}
			copied[k] = copiedVal
		}
		return copied, true
	case []any:
		if v == nil {
			return nil, true
		}
		ptr := reflect.ValueOf(v).Pointer()
		if cached, ok := visited[ptr]; ok {
			return cached, true
		}
		copied := make([]any, len(v))
		visited[ptr] = copied
		for i := range v {
			copied[i] = jsonSafeCopyWithVisited(v[i], visited)
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
	case time.Time:
		return v, true
	}
	return nil, false
}

// jsonSafeReflect is like deepCopyReflect but converts structs that
// contain non-serializable fields into map[string]any representations
// so that the result is always safe for json.Marshal.
func jsonSafeReflect(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	if !rv.IsValid() {
		return nil
	}
	if out, ok := deepCopyByReflectValue(rv); ok {
		return out
	}
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return jsonSafeReflect(rv.Elem(), visited)
	case reflect.Ptr:
		return jsonSafeCopyPointer(rv, visited)
	case reflect.Map:
		return jsonSafeCopyMap(rv, visited)
	case reflect.Slice:
		return jsonSafeCopySlice(rv, visited)
	case reflect.Array:
		return jsonSafeCopyArray(rv, visited)
	case reflect.Struct:
		return jsonSafeCopyStruct(rv, visited)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		// Drop non-serializable values entirely.
		return nil
	default:
		return rv.Interface()
	}
}

func jsonSafeCopyPointer(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	if rv.IsNil() {
		return nil
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}

	// Cache a placeholder before descending to break pointer cycles.
	visited[ptr] = nil
	inner := jsonSafeReflect(rv.Elem(), visited)
	if inner == nil {
		return nil
	}
	newPtr := reflect.New(reflect.TypeOf(inner))
	newPtr.Elem().Set(reflect.ValueOf(inner))
	result := newPtr.Interface()
	visited[ptr] = result
	return result
}

func jsonSafeCopyMap(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	if rv.IsNil() {
		return nil
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	newMap := make(map[string]any, rv.Len())
	visited[ptr] = newMap
	for _, mk := range rv.MapKeys() {
		mv := rv.MapIndex(mk)
		val := jsonSafeReflect(mv, visited)
		if val == nil && mapValueIsJSONUnsafe(mv) {
			continue // Skip non-serializable map values.
		}
		newMap[fmt.Sprint(mk.Interface())] = val
	}
	return newMap
}

func jsonSafeCopySlice(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	if rv.IsNil() {
		return nil
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	l := rv.Len()
	result := make([]any, l)
	visited[ptr] = result
	for i := 0; i < l; i++ {
		result[i] = jsonSafeReflect(rv.Index(i), visited)
	}
	return result
}

func jsonSafeCopyArray(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	l := rv.Len()
	result := make([]any, l)
	for i := 0; i < l; i++ {
		result[i] = jsonSafeReflect(rv.Index(i), visited)
	}
	return result
}

// jsonSafeCopyStruct converts a struct to map[string]any when it
// contains non-serializable fields; otherwise deep-copies normally.
func jsonSafeCopyStruct(
	rv reflect.Value,
	visited map[uintptr]any,
) any {
	if isTimeType(rv.Type()) {
		return copyTime(rv)
	}
	unsafe := hasJSONUnsafeField(rv.Type())
	if unsafe {
		return structToJSONSafeMap(rv, visited)
	}
	// No unsafe fields; deep-copy preserving original type.
	return copyStruct(rv, visited)
}

// structToJSONSafeMap converts a struct value into a map[string]any,
// skipping fields whose types are not JSON-serializable.
func structToJSONSafeMap(
	rv reflect.Value,
	visited map[uintptr]any,
) map[string]any {
	result := make(map[string]any, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		ft := rv.Type().Field(i)
		if shouldSkipJSONField(ft) {
			continue
		}
		if isJSONUnsafeKind(ft.Type.Kind()) {
			continue // Skip chan/func/unsafe-pointer fields.
		}
		key := ft.Name
		if tag := ft.Tag.Get("json"); tag != "" {
			parts := splitJSONTag(tag)
			if parts[0] != "" {
				key = parts[0]
			}
		}
		result[key] = jsonSafeReflect(rv.Field(i), visited)
	}
	return result
}

// shouldSkipJSONField reports whether a struct field should be ignored
// when checking or generating JSON-safe outputs.
func shouldSkipJSONField(ft reflect.StructField) bool {
	if ft.PkgPath != "" {
		return true
	}
	tag := ft.Tag.Get("json")
	if tag == "" {
		return false
	}
	parts := splitJSONTag(tag)
	return parts[0] == "-"
}

// mapValueIsJSONUnsafe reports whether a map value is a non-serializable
// value that should be removed from JSON-safe output.
func mapValueIsJSONUnsafe(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	return isJSONUnsafeKind(value.Kind())
}

// splitJSONTag splits a json struct tag value on commas.
func splitJSONTag(tag string) []string {
	idx := 0
	for idx < len(tag) && tag[idx] != ',' {
		idx++
	}
	if idx == len(tag) {
		return []string{tag}
	}
	return []string{tag[:idx], tag[idx+1:]}
}
