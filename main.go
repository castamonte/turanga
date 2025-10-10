// main.go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"turanga/config"
	"turanga/nostr"
	"turanga/opds"
	"turanga/scanner"
	"turanga/web"

	_ "github.com/mattn/go-sqlite3"
)

// Nostr Reconnect Constants
const (
	initialNostrReconnectDelay = 10 * time.Second // Начальная задержка
	maxNostrReconnectDelay     = 10 * time.Minute // Максимальная задержка
	nostrReconnectResetTimeout = 60 * time.Minute // Время для сброса задержки
)

func main() {
	// Определяем путь к директории исполняемого файла
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Не удалось определить путь к исполняемому файлу: %v", err)
	}
	rootPath := filepath.Dir(exePath)
	log.Printf("Каталог приложения: %s", rootPath)

	// Настройка логирования в файл с ротацией
	logFilePath := filepath.Join(rootPath, "turanga.log")
	logFileOldPath := logFilePath + ".old"

	// Проверяем существование текущего лог-файла
	if _, err := os.Stat(logFilePath); err == nil {
		// Если файл существует, переименовываем его
		err = os.Rename(logFilePath, logFileOldPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Не удалось переименовать старый лог-файл: %v\n", err)
			// Продолжаем работу, даже если не удалось переименовать
		} else {
			log.Printf("Старый лог-файл переименован в %s", logFileOldPath)
		}
	} else if !os.IsNotExist(err) {
		// Если произошла другая ошибка, отличная от "файл не существует"
		fmt.Fprintf(os.Stderr, "Ошибка при проверке лог-файла: %v\n", err)
	}

	// Открываем новый лог-файл
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось открыть файл лога %s: %v\n", logFilePath, err)
		// Продолжаем работу, логи пойдут только в stdout
	} else {
		// Создаем MultiWriter, который пишет и в stdout, и в файл
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		// Устанавливаем стандартный логгер для использования MultiWriter
		log.SetOutput(multiWriter)
		// Опционально: можно установить флаги для более подробного лога
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

		// Не забудьте закрыть файл при завершении программы
		defer func() {
			if logFile != nil {
				logFile.Close()
			}
		}()
	}

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig(filepath.Join(rootPath, "turanga.conf"))
	if err != nil {
		log.Printf("Ошибка загрузки конфигурации: %v", err)
		// Продолжаем с конфигом по умолчанию
		cfg = config.DefaultConfig()
	}

	// Валидируем конфигурацию
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Ошибка валидации конфигурации: %v", err)
	}

	config.SetGlobalConfig(cfg)

	log.Println("Используемая конфигурация:")
	log.Println(cfg.String())

	if cfg.Debug {
		log.Println("Инициализация базы данных...")
	}
	initDB(rootPath)

	// Создаем контекст с отменой для управления жизненным циклом приложения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Гарантируем отмену контекста при выходе из main

	// Инициализируем Nostr клиент
	var nostrClient *nostr.Client
	nostrClient, err = nostr.NewClient(cfg, db)
	if err != nil {
		log.Printf("Предупреждение: Ошибка инициализации Nostr клиента: %v", err)
		// Продолжаем работу, Nostr может быть не критичен для основного функционала
		nostrClient = nil // Установка в nil при ошибке
	}

	// Передаем DB и конфигурацию в scanner
	scanner.SetDB(db)
	scanner.SetConfig(cfg)
	scanner.SetRootPath(rootPath)

	// Устанавливаем корневую директорию для OPDS
	opds.SetRootPath(rootPath)

	// Выполняем очистку перед сканированием
	err = scanner.CleanupMissingFiles()
	if err != nil {
		log.Printf("Предупреждение: ошибка при очистке отсутствующих файлов: %v", err)
		// Не останавливаем выполнение из-за ошибки очистки
	}

	// Создаем экземпляр веб-интерфейса один раз при запуске
	webInterface := web.NewWebInterface(db, cfg, nostrClient, rootPath)

	// Создаем экземпляры обработчиков из пакета handlers
	bookHandler := opds.NewBookHandler(db, cfg)
	authorHandler := opds.NewAuthorHandler(db, cfg)
	seriesHandler := opds.NewSeriesHandler(db, cfg)
	tagHandler := opds.NewTagHandler(db, cfg)

	// --- Запуск Nostr Subscription Manager с переподключением ---
	// Создаем и запускаем подписку на запросы книг через Nostr в отдельной горутине
	// с механизмом автоматического перезапуска.
	if nostrClient != nil && nostrClient.IsEnabled() {
		// Запускаем менеджер подписки в отдельной горутине
		go runNostrSubscriptionManager(ctx, nostrClient, cfg)
		if cfg.Debug {
			log.Println("Nostr Subscription Manager goroutine started.")
		}

		// Горутина для первой очистки через 1 минуту после старта запускается один раз
		// Она не зависит от конкретного экземпляра subManager, так как CleanupOldEvents
		// работает напрямую с БД.
		go func() {
			select {
			case <-time.After(1 * time.Minute):
				// Создаем временный экземпляр для вызова статического метода
				tempSubManager := nostr.NewSubscriptionManager(nostrClient, cfg, db)
				tempSubManager.CleanupOldEvents()
				if cfg.Debug {
					log.Println("Проведена первая очистка старых событий nostr.")
				}
			case <-ctx.Done():
				if cfg.Debug {
					log.Println("Контекст завершен до первой очистки событий nostr, горутина завершается.")
				}
				return
			}
		}()

	} else {
		if cfg.Debug {
			log.Println("Nostr клиент не инициализирован или отключен, подписка на запросы книг пропущена")
		}
	}
	// --- Конец Nostr Subscription Manager ---

	// --- Добавляем Graceful Shutdown ---
	// Запускаем отдельную горутину для обработки сигналов ОС (например, Ctrl+C)
	go func() {
		// Ожидаем сигнал завершения (например, SIGINT, SIGTERM)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		sig := <-sigChan
		if cfg.Debug {
			log.Printf("Получен сигнал завершения: %v", sig)
		}

		// Отменяем основной контекст, сигнализируя всем зависимым горутинам о необходимости завершиться
		cancel()

		// Можно дать немного времени на корректное завершение
		// time.Sleep(2 * time.Second) // Опционально

		// Завершаем программу
		if cfg.Debug {
			log.Println("Завершение работы приложения...")
		}
	}()
	// --- Конец Graceful Shutdown ---

	// Маршруты для API OPDS
	http.HandleFunc("/", opds.IndexHandler(webInterface, cfg))
	http.HandleFunc("/feed", opds.ShowOPDSCatalogHandler)
	http.HandleFunc("/books", bookHandler.BooksHandler)
	http.HandleFunc("/books/", bookHandler.BooksHandler)
	http.HandleFunc("/authors", authorHandler.AuthorsHandler)
	http.HandleFunc("/authors/", authorHandler.AuthorsHandler)
	http.HandleFunc("/series", seriesHandler.SeriesHandler)
	http.HandleFunc("/series/", seriesHandler.SeriesHandler)
	http.HandleFunc("/recent", bookHandler.RecentHandler)
	http.HandleFunc("/tags", tagHandler.TagsHandler)
	http.HandleFunc("/tags/", tagHandler.TagsHandler)
	http.HandleFunc("/opds-search/", opds.OPDSSearchHandler(webInterface))
	http.HandleFunc("/opds-download/", opds.OPDSDownloadBookHandler(db, rootPath))

	// Маршруты для веб-интерфейса
	http.HandleFunc("/author/", webInterface.ShowAuthorHandler)
	http.HandleFunc("/s/", webInterface.ShowSeriesHandler)
	http.HandleFunc("/save/author/", webInterface.SaveAuthorHandler)
	http.HandleFunc("/save/series/", webInterface.SaveSeriesHandler)
	http.HandleFunc("/save/book/", webInterface.SaveBookFieldHandler)
	http.HandleFunc("/tag/", webInterface.ShowTagHandler)
	http.HandleFunc("/delete/book/", webInterface.DeleteBookHandler)
	http.HandleFunc("/upload", webInterface.UploadBookHandler)
	http.HandleFunc("/auth", webInterface.AuthHandler)
	http.HandleFunc("/logout", webInterface.LogoutHandler)
	http.HandleFunc("/request", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			webInterface.ShowRequestFormHandler(w, r)
		case http.MethodPost:
			webInterface.HandleRequestFormHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/blacklist/add", webInterface.AddToBlacklistHandler)
	http.HandleFunc("/download/ipfs/", webInterface.DownloadIPFSBookHandler)
	http.HandleFunc("/request/check-updates", func(w http.ResponseWriter, r *http.Request) {
		webInterface.CheckUpdatesHandler(w, r)
	})
	http.HandleFunc("/request/check-updates-detailed", func(w http.ResponseWriter, r *http.Request) {
		webInterface.CheckUpdatesDetailedHandler(w, r)
	})
	http.HandleFunc("/request/response-count", func(w http.ResponseWriter, r *http.Request) {
		webInterface.ResponseCountHandler(w, r)
	})
	http.HandleFunc("/revision", webInterface.RevisionHandler)
	http.HandleFunc("/revision/progress", webInterface.ProgressHandler)

	// Статические файлы
	staticDir := filepath.Join(rootPath, "web", "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	// Все операции с тегами книги: POST, DELETE, GET /tags/book/{id}
	http.HandleFunc("/tags/book/", tagHandler.HandleTagBookRequest)

	// Маршрут для identicon
	http.HandleFunc("/identicon/", webInterface.ShowIdenticon)

	// Маршрут для страницы деталей книги
	http.HandleFunc("/book/", webInterface.ShowBookDetailHandler)

	// Маршрут для отдачи обложек
	coversDirAbs := filepath.Join(rootPath, "covers")
	http.Handle("/covers/", http.StripPrefix("/covers/", http.FileServer(http.Dir(coversDirAbs))))

	// Mаршрут для запроса книги через Nostr
	http.HandleFunc("/request/book/", webInterface.RequestBookViaNostrHandler)

	if cfg.Debug {
		log.Printf("OPDS сервер запущен на порту :%d", cfg.Port)
	}

	// Запускаем HTTP-сервер, используя контекст с отменой
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: nil, // Обработчики уже зарегистрированы через http.HandleFunc
	}

	// Запускаем сервер в отдельной горутине, чтобы иметь возможность отменить его через контекст
	go func() {
		// Ожидаем отмены контекста
		<-ctx.Done()
		if cfg.Debug {
			log.Println("Контекст отменен, останавливаем HTTP-сервер...")
		}
		// Используем Shutdown для корректного завершения
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка при остановке HTTP-сервера: %v", err)
			}
			// Принудительное завершение, если Shutdown не помог
			server.Close()
		}
	}()

	// Запускаем сервер (блокирует выполнение до тех пор, пока ctx.Done() не сработает или не будет ошибки)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		if cfg.Debug {
			log.Printf("Ошибка HTTP-сервера: %v", err)
		}
		// Отменяем контекст в случае ошибки сервера
		cancel()
	}

	// Ожидаем завершения контекста (это нужно, если server.ListenAndServe вернул http.ErrServerClosed)
	<-ctx.Done()
	log.Println("Приложение завершено.")
}

// runNostrSubscriptionManager запускает SubscriptionManager и управляет его перезапуском.
func runNostrSubscriptionManager(ctx context.Context, nostrClient *nostr.Client, cfg *config.Config) {
	var lastErrorTime time.Time
	currentReconnectDelay := initialNostrReconnectDelay
	var subManager *nostr.SubscriptionManager
	var stopCh chan struct{}

	for {
		select {
		case <-ctx.Done():
			log.Println("Nostr Subscription Manager: received global context cancel, shutting down.")
			// Убедимся, что предыдущая подписка остановлена
			if stopCh != nil {
				close(stopCh)
				stopCh = nil // Обнуляем, чтобы не закрыть дважды
			}
			return
		default:
		}

		// --- Попытка запуска ---
		if cfg.Debug {
			log.Println("Nostr Subscription Manager: attempting to start...")
		}

		// Создаем новый экземпляр SubscriptionManager
		subManager = nostr.NewSubscriptionManager(nostrClient, cfg, db)

		var err error
		// Запускаем подписку
		stopCh, err = subManager.Start(ctx) // Передаем основной контекст приложения

		if err != nil {
			now := time.Now()
			if cfg.Debug {
				log.Printf("Nostr Subscription Manager: failed to start: %v", err)
			}

			// Логика сброса задержки
			if now.Sub(lastErrorTime) > nostrReconnectResetTimeout {
				currentReconnectDelay = initialNostrReconnectDelay
				if cfg.Debug {
					log.Printf("Nostr Subscription Manager: resetting reconnect delay to %v.", currentReconnectDelay)
				}
			}
			lastErrorTime = now

			if cfg.Debug {
				log.Printf("Nostr Subscription Manager: will retry in %v...", currentReconnectDelay)
			}
			select {
			case <-time.After(currentReconnectDelay):
				// Увеличиваем задержку
				currentReconnectDelay *= 2
				if currentReconnectDelay > maxNostrReconnectDelay {
					currentReconnectDelay = maxNostrReconnectDelay
				}
				if cfg.Debug {
					log.Printf("Nostr Subscription Manager: next reconnect delay will be %v.", currentReconnectDelay)
				}
				continue // Повторяем цикл запуска
			case <-ctx.Done():
				if cfg.Debug {
					log.Println("Nostr Subscription Manager: context cancelled while waiting to retry start.")
				}
				return
			}
		}

		if stopCh == nil {
			if cfg.Debug {
				log.Println("Nostr Subscription Manager: started, but returned nil stop channel. This might indicate it's not configured to run (e.g., client disabled). Will retry shortly.")
			}
			// Даже если stopCh nil, всё равно ждём и пробуем перезапустить,
			// на случай, если конфигурация изменится или клиент активируется.
			// Это предотвращает "горячую" петлю.
		} else {
			if cfg.Debug {
				log.Printf("Nostr Subscription Manager: started successfully.")
			}
		}

		// --- Ожидание завершения или сигнала перезапуска ---
		// Если Start не вернул ошибку, считаем, что подписка активна.

		// Сбросим таймер ошибок, так как запуск был успешным
		// lastErrorTime = time.Time{} // Убираем эту строку, так как переменная больше не используется
		currentReconnectDelay = initialNostrReconnectDelay

		// Запускаем периодическую очистку внутри той же горутины/контекста
		go func(sm *nostr.SubscriptionManager, appCtx context.Context) {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			if cfg.Debug {
				log.Println("Nostr Subscription Manager: запущена ежечасная очистка событий")
			}
			for {
				select {
				case <-ticker.C:
					if cfg.Debug {
						log.Println("Nostr Subscription Manager: выполняется периодическая очистка старых событий...")
					}
					sm.CleanupOldEvents() // Вызываем метод у локального экземпляра
					if cfg.Debug {
						log.Println("Nostr Subscription Manager: периодическая очистка старых событий завершена.")
					}
				case <-appCtx.Done(): // Используем контекст приложения
					if cfg.Debug {
						log.Println("Nostr Subscription Manager: контекст завершен, останавливаем горутину периодической очистки.")
					}
					return
				}
			}
		}(subManager, ctx) // Передаем локальный subManager и глобальный контекст ctx

		// Упрощаем select с одним case
		<-ctx.Done()
		if cfg.Debug {
			log.Println("Nostr Subscription Manager: received global context cancel while running.")
		}
		if stopCh != nil {
			close(stopCh) // Сигнализируем SubscriptionManager остановиться
			stopCh = nil
		}
		return
	}
}
