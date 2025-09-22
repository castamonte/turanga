package nostr

import (
	"bufio"
	"log"
	"os"
	"strings"
	"sync"

	"turanga/config"
)

// Blacklist хранит список заблокированных публичных ключей и хешей файлов
type Blacklist struct {
	pubkeys    map[string]bool
	fileHashes map[string]bool
	mutex      sync.RWMutex
}

// NewBlacklist создает новый экземпляр чёрного списка
func NewBlacklist() *Blacklist {
	return &Blacklist{
		pubkeys:    make(map[string]bool),
		fileHashes: make(map[string]bool),
		mutex:      sync.RWMutex{},
	}
}

// LoadFromFile загружает чёрный список из файла
func (bl *Blacklist) LoadFromFile(filename string) error {
	cfg := config.GetConfig()
	bl.mutex.Lock()
	defer bl.mutex.Unlock()

	// Очищаем существующие списки
	bl.pubkeys = make(map[string]bool)
	bl.fileHashes = make(map[string]bool)

	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Файл чёрного списка %s не найден, создается пустой список", filename)
			}
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		// Пропускаем пустые строки и комментарии
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Определяем тип записи по длине и формату
		if len(line) == 16 { // Длина хеша xxhash
			bl.fileHashes[line] = true
			log.Printf("file_hash %s в чёрный список", line)
		} else if len(line) == 66 && (strings.HasPrefix(line, "npub1") || strings.HasPrefix(line, "0x")) {
			// npub ключ или hex публичный ключ
			bl.pubkeys[line] = true
			log.Printf("pubkey %s в чёрный список", line)
		} else if len(line) > 66 && strings.HasPrefix(line, "npub1") {
			// npub ключ (длинный формат)
			bl.pubkeys[line] = true
			log.Printf("pubkey %sв чёрный список", line)
		} else {
			log.Printf("WARNING: неизвестный формат в строке %d чёрного списка: %s", lineNumber, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if cfg.Debug {
		log.Printf("Чёрный список загружен: %d pubkeys, %d file hashes", len(bl.pubkeys), len(bl.fileHashes))
	}
	return nil
}

// IsPubkeyBlocked проверяет, заблокирован ли публичный ключ
func (bl *Blacklist) IsPubkeyBlocked(pubkey string) bool {
	bl.mutex.RLock()
	defer bl.mutex.RUnlock()

	// Проверяем как hex, так и npub формат
	if bl.pubkeys[pubkey] {
		return true
	}

	// TODO: Добавить проверку npub формата, если pubkey в hex формате
	return false
}

// IsFileHashBlocked проверяет, заблокирован ли хеш файла
func (bl *Blacklist) IsFileHashBlocked(fileHash string) bool {
	bl.mutex.RLock()
	defer bl.mutex.RUnlock()
	return bl.fileHashes[fileHash]
}

// AddPubkey добавляет публичный ключ в чёрный список
func (bl *Blacklist) AddPubkey(pubkey string) {
	bl.mutex.Lock()
	defer bl.mutex.Unlock()
	bl.pubkeys[pubkey] = true
}

// AddFileHash добавляет хеш файла в чёрный список
func (bl *Blacklist) AddFileHash(fileHash string) {
	bl.mutex.Lock()
	defer bl.mutex.Unlock()
	bl.fileHashes[fileHash] = true
}

// SaveToFile сохраняет чёрный список в файл
func (bl *Blacklist) SaveToFile(filename string) error {
	bl.mutex.RLock()
	defer bl.mutex.RUnlock()

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Записываем pubkeys
	for pubkey := range bl.pubkeys {
		if _, err := writer.WriteString(pubkey + "\n"); err != nil {
			return err
		}
	}

	// Записываем file hashes
	for fileHash := range bl.fileHashes {
		if _, err := writer.WriteString(fileHash + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// GetAllBlockedFileHashes возвращает все заблокированные хеши файлов
func (bl *Blacklist) GetAllBlockedFileHashes() []string {
	bl.mutex.RLock()
	defer bl.mutex.RUnlock()

	hashes := make([]string, 0, len(bl.fileHashes))
	for hash := range bl.fileHashes {
		hashes = append(hashes, hash)
	}
	return hashes
}
