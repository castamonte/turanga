package scanner

import (
    "bytes"
    "encoding/json"
    "fmt"
    "os/exec"
    "strings"

    "github.com/gabriel-vasile/mimetype"
)

func ExtractPDFMetadata(filePath string) (author, title string, err error) {
    // Проверяем MIME тип
    mtype, err := mimetype.DetectFile(filePath)
    if err != nil {
        return "", "", fmt.Errorf("ошибка определения типа файла: %v", err)
    }
    
    if !strings.Contains(mtype.String(), "pdf") {
        return "", "", fmt.Errorf("файл не является PDF: %s", mtype.String())
    }
    
    // Пробуем извлечь метаданные с помощью exiftool
    author, title, err = extractPDFMetadataWithExiftool(filePath)
    if err == nil && title != "" {
        return author, title, nil
    }
    fmt.Printf("Exiftool не сработал: %v\n", err)
    
    // Альтернативный метод с pdfinfo
    author, title, err = extractPDFMetadataWithPdfinfo(filePath)
    if err == nil && title != "" {
        return author, title, nil
    }
    fmt.Printf("Pdfinfo не сработал: %v\n", err)
    
    // Метод с pdftotext для извлечения заголовка из первой страницы
    title, err = extractPDFTitleWithPdftotext(filePath)
    if err == nil && title != "" {
        return "", title, nil
    }
    fmt.Printf("Pdftotext не сработал: %v\n", err)
    
    return "", "", fmt.Errorf("не удалось извлечь метаданные из PDF")
}

func extractPDFMetadataWithExiftool(filePath string) (author, title string, err error) {
    // Проверяем наличие exiftool
    _, err = exec.LookPath("exiftool")
    if err != nil {
        return "", "", fmt.Errorf("exiftool не найден в системе")
    }
    
    cmd := exec.Command("exiftool", "-Author", "-Title", "-s", "-j", filePath)
    var out bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    if err != nil {
        return "", "", fmt.Errorf("exiftool ошибка: %v, stderr: %s", err, stderr.String())
    }
    
    // Парсим JSON
    var metadata []map[string]interface{}
    err = json.Unmarshal(out.Bytes(), &metadata)
    if err != nil {
        return "", "", fmt.Errorf("ошибка парсинга JSON: %v", err)
    }
    
    if len(metadata) > 0 {
        if titleVal, ok := metadata[0]["Title"].(string); ok {
            title = strings.TrimSpace(titleVal)
        }
        if authorVal, ok := metadata[0]["Author"].(string); ok {
            author = strings.TrimSpace(authorVal)
        }
    }
    
    if title == "" {
        return "", "", fmt.Errorf("пустое название в exiftool")
    }
    
    return author, title, nil
}

func extractPDFMetadataWithPdfinfo(filePath string) (author, title string, err error) {
    // Проверяем наличие pdfinfo
    _, err = exec.LookPath("pdfinfo")
    if err != nil {
        return "", "", fmt.Errorf("pdfinfo не найден в системе")
    }
    
    cmd := exec.Command("pdfinfo", filePath)
    var out bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    if err != nil {
        return "", "", fmt.Errorf("pdfinfo ошибка: %v, stderr: %s", err, stderr.String())
    }
    
    output := out.String()
    lines := strings.Split(output, "\n")
    
    for _, line := range lines {
        if strings.HasPrefix(line, "Title:") {
            title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
        } else if strings.HasPrefix(line, "Author:") {
            author = strings.TrimSpace(strings.TrimPrefix(line, "Author:"))
        }
    }
    
    if title == "" {
        return "", "", fmt.Errorf("пустое название в pdfinfo")
    }
    
    return author, title, nil
}

func extractPDFTitleWithPdftotext(filePath string) (title string, err error) {
    // Проверяем наличие pdftotext
    _, err = exec.LookPath("pdftotext")
    if err != nil {
        return "", fmt.Errorf("pdftotext не найден в системе")
    }
    
    // Извлекаем первые 2000 символов первой страницы
    cmd := exec.Command("pdftotext", "-l", "1", "-layout", filePath, "-")
    var out bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    if err != nil {
        return "", fmt.Errorf("pdftotext ошибка: %v, stderr: %s", err, stderr.String())
    }
    
    // Получаем первые строки как потенциальный заголовок
    content := out.String()
    lines := strings.Split(content, "\n")
    
    // Ищем непустую строку в начале документа
    for i := 0; i < min(5, len(lines)); i++ {
        line := strings.TrimSpace(lines[i])
        if len(line) > 10 && len(line) < 200 { // Разумная длина заголовка
            // Убираем специальные символы
            title = strings.TrimSpace(strings.Split(line, "\n")[0])
            if title != "" {
                break
            }
        }
    }
    
    if title == "" {
        return "", fmt.Errorf("не удалось извлечь заголовок из текста")
    }
    
    return title, nil
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
