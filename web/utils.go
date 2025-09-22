// web/utils.go
package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/cespare/xxhash/v2"
)

// HashPassword создает xxhash от строки пароля
func HashPassword(password string) string {
	h := xxhash.Sum64String(password)
	return fmt.Sprintf("%016x", h)
}

// GenerateRandomPassword генерирует случайный пароль
func GenerateRandomPassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

// FormatFileSize форматирует размер файла в удобочитаемый формат
func FormatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d Б", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1f КБ", float64(size)/1024)
	} else if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1f МБ", float64(size)/(1024*1024))
	} else {
		return fmt.Sprintf("%.1f ГБ", float64(size)/(1024*1024*1024))
	}
}
