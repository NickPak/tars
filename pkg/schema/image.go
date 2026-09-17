package schema

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// 图片 data URL（Message.Images 的元素形态）的解析与落盘机制。
// 交互职责（保存对话框、默认文件名）在服务层。

// ParseImageDataURL 解析 data:image/<mime>;base64,<data> 形式的 data URL，
// 返回 MIME 类型、文件扩展名（含点）与解码后的字节。
func ParseImageDataURL(dataURL string) (mime, ext string, data []byte, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", "", nil, fmt.Errorf("not a data URL")
	}
	head, b64, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasSuffix(head, ";base64") {
		return "", "", nil, fmt.Errorf("invalid image data URL format")
	}
	mime = strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	switch mime {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return "", "", nil, fmt.Errorf("unsupported image type: %s", mime)
	}
	data, err = base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", "", nil, fmt.Errorf("decode image data: %w", err)
	}
	return mime, ext, data, nil
}

// WriteImage 把图片 data URL 解码并写入 target（保存图片机制的落盘实现）。
// 返回实际扩展名（供调用方生成默认文件名等展示用途）。
func WriteImage(dataURL, target string) (string, error) {
	_, ext, data, err := ParseImageDataURL(dataURL)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, data, 0644); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}
	return ext, nil
}
