// web/auth.go
package web

import (
	"html/template"
	"log"
	"net/http"
	"path/filepath"
)

// AuthHandler обрабатывает запросы аутентификации
func (w *WebInterface) AuthHandler(wr http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Показываем форму ввода пароля
		w.showAuthForm(wr, r, "")
	case http.MethodPost:
		// Проверяем пароль
		w.checkPassword(wr, r)
	default:
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// showAuthForm показывает форму ввода пароля
func (w *WebInterface) showAuthForm(wr http.ResponseWriter, r *http.Request, errorMessage string) {
	data := struct {
		ErrorMessage string
	}{
		ErrorMessage: errorMessage,
	}

	// Загружаем шаблон из файла
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "auth.html")
	tmpl, err := template.New("auth.html").Funcs(template.FuncMap{
		"formatSize": FormatFileSize, // Добавлено, на случай если понадобится
	}).ParseFiles(tmplPath)

	if err != nil {
		log.Printf("Error parsing auth template: %v", err)
		http.Error(wr, "Template error", http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "auth", data); err != nil {
		log.Printf("Error executing auth template: %v", err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// checkPassword проверяет введенный пароль
func (w *WebInterface) checkPassword(wr http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	if password == "" {
		w.showAuthForm(wr, r, "Пароль не может быть пустым")
		return
	}

	// Если хэш пароля еще не установлен
	if w.config.PasswordHash == "" {
		// Создаем новый хэш и сохраняем в конфиг
		hash := HashPassword(password)
		w.config.PasswordHash = hash
		if err := w.config.SaveConfig("turanga.conf"); err != nil {
			log.Printf("Error saving config: %v", err)
			w.showAuthForm(wr, r, "Ошибка сохранения конфигурации")
			return
		}
		// Устанавливаем cookie и перенаправляем
		http.SetCookie(wr, &http.Cookie{
			Name:  "auth",
			Value: hash,
			Path:  "/",
		})
		http.Redirect(wr, r, "/", http.StatusSeeOther)
		return
	}

	// Проверяем хэш
	inputHash := HashPassword(password)
	if inputHash != w.config.PasswordHash {
		w.showAuthForm(wr, r, "Неверный пароль")
		return
	}

	// Устанавливаем cookie
	http.SetCookie(wr, &http.Cookie{
		Name:  "auth",
		Value: inputHash,
		Path:  "/",
	})

	// Перенаправляем на главную
	http.Redirect(wr, r, "/", http.StatusSeeOther)
}

// logoutHandler обрабатывает выход из системы
func (w *WebInterface) LogoutHandler(wr http.ResponseWriter, r *http.Request) {
	// Удаляем cookie
	http.SetCookie(wr, &http.Cookie{
		Name:   "auth",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	// Перенаправляем на главную
	http.Redirect(wr, r, "/", http.StatusSeeOther)
}
