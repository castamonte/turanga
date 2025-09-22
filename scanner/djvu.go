package scanner

import (
    "bytes"
    "fmt"
    "os"
    "os/exec"
    "strings"
)

func ExtractDJVUMetadata(filePath string) (author, title string, err error) {
    // Пробуем извлечь метаданные с помощью djvused
    author, title, err = extractDJVUMetadataWithDjvused(filePath)
    if err == nil && (title != "" || author != "") {
        return author, title, nil
    }
    fmt.Printf("Djvused не сработал: %v\n", err)
    
    // Пробуем извлечь информацию с помощью djvudump
    title, err = extractDJVUMetadataWithDjvudump(filePath)
    if err == nil && title != "" {
        return "", title, nil
    }
    fmt.Printf("Djvudump не сработал: %v\n", err)
    
    // Пробуем извлечь текст с первой страницы как запасной вариант
    title, err = extractDJVUTitleWithDjvutxt(filePath)
    if err == nil && title != "" {
        return "", title, nil
    }
    fmt.Printf("Djvutxt не сработал: %v\n", err)
    
    return "", "", fmt.Errorf("не удалось извлечь метаданные из DJVU")
}

func extractDJVUMetadataWithDjvused(filePath string) (author, title string, err error) {
    // Проверяем наличие djvused
    _, err = exec.LookPath("djvused")
    if err != nil {
        return "", "", fmt.Errorf("djvused не найден в системе")
    }
    
    // Пытаемся извлечь метаданные
    cmd := exec.Command("djvused", "-e", "print-meta", filePath)
    var out bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    if err != nil {
        return "", "", fmt.Errorf("djvused ошибка: %v, stderr: %s", err, stderr.String())
    }
    
    output := out.String()
    
    // Ищем метаданные в выводе
    lines := strings.Split(output, "\n")
    for _, line := range lines {
        if strings.Contains(strings.ToUpper(line), "TITLE") {
            // Извлекаем значение после знака =
            parts := strings.SplitN(line, "=", 2)
            if len(parts) == 2 {
                title = strings.TrimSpace(strings.Trim(parts[1], `"`))
                title = strings.TrimSpace(strings.Trim(title, `'`))
            }
        } else if strings.Contains(strings.ToUpper(line), "AUTHOR") {
            parts := strings.SplitN(line, "=", 2)
            if len(parts) == 2 {
                author = strings.TrimSpace(strings.Trim(parts[1], `"`))
                author = strings.TrimSpace(strings.Trim(author, `'`))
            }
        }
    }
    
    // Если не нашли метаданные, пробуем другой формат
    if title == "" && author == "" {
        // Ищем аннотации
        cmd = exec.Command("djvused", "-e", "print-ant", filePath)
        cmd.Stdout = &out
        cmd.Stderr = &stderr
        
        err = cmd.Run()
        if err == nil {
            output = out.String()
            // Простой парсинг аннотаций
            if strings.Contains(output, "<title>") {
                start := strings.Index(output, "<title>") + 7
                end := strings.Index(output[start:], "</title>")
                if end != -1 {
                    title = strings.TrimSpace(output[start : start+end])
                }
            }
        }
    }
    
    return author, title, nil
}

func extractDJVUMetadataWithDjvudump(filePath string) (title string, err error) {
    // Проверяем наличие djvudump
    _, err = exec.LookPath("djvudump")
    if err != nil {
        return "", fmt.Errorf("djvudump не найден в системе")
    }
    
    // Получаем информацию о файле
    cmd := exec.Command("djvudump", filePath)
    var out bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    if err != nil {
        return "", fmt.Errorf("djvudump ошибка: %v, stderr: %s", err, stderr.String())
    }
    
    output := out.String()
    
    // Ищем информацию о документе
    lines := strings.Split(output, "\n")
    for _, line := range lines {
        if strings.Contains(line, "DjVu") && strings.Contains(line, "file") {
            // Извлекаем имя файла как потенциальный заголовок
            // Это запасной вариант
            break
        }
    }
    
    return "", fmt.Errorf("метаданные не найдены в djvudump")
}

func extractDJVUTitleWithDjvutxt(filePath string) (title string, err error) {
    // Проверяем наличие djvutxt или djvused для извлечения текста
    _, err = exec.LookPath("djvutxt")
    if err != nil {
        // Пробуем альтернативный способ с djvused
        _, err = exec.LookPath("djvused")
        if err != nil {
            return "", fmt.Errorf("djvutxt и djvused не найдены в системе")
        }
        
        // Извлекаем текст с первой страницы через djvused
        cmd := exec.Command("djvused", "-e", "select 1; print-txt", filePath)
        var out bytes.Buffer
        var stderr bytes.Buffer
        cmd.Stdout = &out
        cmd.Stderr = &stderr
        
        err = cmd.Run()
        if err != nil {
            return "", fmt.Errorf("djvused текст ошибка: %v, stderr: %s", err, stderr.String())
        }
        
        output := out.String()
        lines := strings.Split(output, "\n")
        
        // Ищем первую строку с текстом
        for _, line := range lines {
            trimmed := strings.TrimSpace(line)
            if len(trimmed) > 5 && len(trimmed) < 200 && !strings.Contains(trimmed, "(") {
                title = trimmed
                break
            }
        }
    } else {
        // Используем djvutxt
        cmd := exec.Command("djvutxt", "-page", "1", filePath)
        var out bytes.Buffer
        var stderr bytes.Buffer
        cmd.Stdout = &out
        cmd.Stderr = &stderr
        
        err = cmd.Run()
        if err != nil {
            return "", fmt.Errorf("djvutxt ошибка: %v, stderr: %s", err, stderr.String())
        }
        
        output := out.String()
        lines := strings.Split(output, "\n")
        
        // Ищем первую строку с текстом
        for _, line := range lines {
            trimmed := strings.TrimSpace(line)
            if len(trimmed) > 5 && len(trimmed) < 200 {
                title = trimmed
                break
            }
        }
    }
    
    if title == "" {
        return "", fmt.Errorf("не удалось извлечь заголовок из текста")
    }
    
    return title, nil
}

func extractDJVUMetadataFallback(filePath string) (author, title string, err error) {
    // Простой fallback - проверяем наличие файла и используем имя файла
    _, err = os.Stat(filePath)
    if err != nil {
        return "", "", err
    }
    
    // Для fallback возвращаем пустые значения, чтобы использовался метод из имени файла
    return "", "", fmt.Errorf("fallback - используйте имя файла")
}
