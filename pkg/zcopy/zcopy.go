package zcopy

import "unsafe"

const (
	EmptyString string = ""
)

// UnsafeStringToBytes 由于 Go 字符串是不可变的，因此 UnsafeStringToBytes 返回的字节不能被修改。
func UnsafeStringToBytes(s string) []byte {
	if len(s) <= 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// UnsafeBytesToString 由于 Go 字符串是不可变的，因此只要返回的字符串值存在，就不能修改传递给 String 的底层字节切片。
func UnsafeBytesToString(b []byte) string {
	if len(b) <= 0 {
		return EmptyString
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}
