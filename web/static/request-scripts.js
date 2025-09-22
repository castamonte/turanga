// web/static/request-scripts.js

// Обернем код в функцию, чтобы избежать глобальных переменных
(function() {
    'use strict';

    // --- Переменные ---
    let isPollingForResponses = false; // Флаг для предотвращения множественных опросов

    // --- Функции ---

    // Функция для скачивания через IPFS
    function downloadFromIPFS(fileHash, ipfsCID, fileType, title) {
        // Находим кнопку, которая была нажата
        // ВНИМАНИЕ: event.target может быть недоступен, если функция вызвана не через обработчик события.
        // Лучше передавать кнопку как аргумент или использовать currentTarget в обработчике.
        // Но для совместимости с оригинальным кодом оставим так.
        // Предполагается, что event доступен в контексте вызова.
        const button = event ? (event.target.closest('button') || event.currentTarget) : null;
        let originalHTML = '';
        if (button) {
            originalHTML = button.innerHTML;
            // Показываем индикатор загрузки
            button.innerHTML = '<i class="fas fa-spinner fa-spin"></i>';
            button.disabled = true;
        }

        // Отправляем запрос на сервер
        fetch('/download/ipfs/', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                'file_hash': fileHash,
                'ipfs_cid': ipfsCID,
                'file_type': fileType,
                'title': title
            })
        })
        .then(response => {
            // Проверяем, является ли ответ JSON
            const contentType = response.headers.get("content-type");
            if (contentType && contentType.indexOf("application/json") !== -1) {
                return response.json();
            } else {
                // Если не JSON, пробуем получить текст для отладки
                return response.text().then(text => {
                    console.error("Ответ сервера не является JSON:", text);
                    throw new Error(`Сервер вернул неожиданный ответ: ${response.status} ${response.statusText}`);
                });
            }
        })
        .then(data => {
            if (data.success) {
                // Успешно скачано - заменяем кнопку на галочку с атрибутами для перехода
                if (button) {
                    const newButton = document.createElement('span');
                    newButton.className = 'btn btn-sm btn-outline-success local-book-link';
                    // Проверяем, есть ли book_id в ответе
                    if (data.book_id) {
                        newButton.setAttribute('data-book-id', data.book_id);
                        newButton.title = 'Есть локально. Кликните, чтобы перейти к книге.';
                    } else {
                        newButton.title = 'Есть локально';
                    }
                    newButton.innerHTML = '<i class="fas fa-check"></i>';
                    
                    // Добавляем обработчик клика для новой галочки (только если есть book_id)
                    if (data.book_id) {
                        newButton.addEventListener('click', function() {
                            const bookID = this.getAttribute('data-book-id');
                            const bookIDInt = parseInt(bookID, 10);
                            if (!isNaN(bookIDInt) && bookIDInt > 0) {
                                goToLocalBook(bookIDInt);
                            } else {
                                console.warn("Invalid Book ID from data attribute:", bookID);
                                alert("Некорректный ID книги.");
                            }
                        });
                    }
            
                    button.parentNode.replaceChild(newButton, button);
            
                    // Находим и обновляем идентикон в той же строке, если он есть
                    const row = newButton.closest('tr');
                    if (row) {
                        const identicon = row.querySelector('img[src*="/identicon/"]');
                        if (identicon) {
                            // Добавляем визуальное выделение идентикона
                            identicon.style.border = '2px solid #28a745';
                            identicon.style.borderRadius = '4px';
                        }
                    }
                }
            
                showSuccessMessage('Файл скачан и добавлен в библиотеку');
            } else {
                // Ошибка - восстанавливаем кнопку
                if (button) {
                    button.innerHTML = originalHTML;
                    button.disabled = false;
                }
                alert('Ошибка: ' + (data.message || 'Неизвестная ошибка от сервера'));
            }
        })
        .catch(error => {
            console.error('Error downloading from IPFS:', error);
            // Восстанавливаем кнопку в случае ошибки
            if (button) {
                button.innerHTML = originalHTML;
                button.disabled = false;
            }
            alert('Ошибка скачивания файла: ' + error.message);
        });
    }

    // Функция для показа сообщения об успехе
    function showSuccessMessage(message) {
        // Создаем элемент для сообщения
        const alertDiv = document.createElement('div');
        alertDiv.className = 'alert alert-success alert-dismissible fade show';
        alertDiv.role = 'alert';
        alertDiv.innerHTML = `
            ${message}
            <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
        `;
        alertDiv.style.marginTop = '10px'; // Добавим немного отступа

        // Добавляем сообщение в начало страницы
        const container = document.querySelector('.container');
        if (container) {
            // Вставляем после первого дочернего элемента или в начало, если его нет
            if (container.firstChild) {
                container.insertBefore(alertDiv, container.firstChild);
            } else {
                container.appendChild(alertDiv);
            }

            // Автоматически скрываем сообщение через 3 секунды
            setTimeout(() => {
                if (alertDiv.parentNode) {
                    // Проверяем, доступен ли Bootstrap
                    if (typeof bootstrap !== 'undefined' && bootstrap.Alert) {
                        const bsAlert = new bootstrap.Alert(alertDiv);
                        bsAlert.close();
                    } else {
                        // Если Bootstrap недоступен, просто удаляем элемент
                        alertDiv.remove();
                    }
                }
            }, 2000);
        }
    }

    // Функция для добавления в черный список
    function addToBlacklist(fileHash, pubkey) {
        if (!fileHash && !pubkey) {
            alert('Нет данных для добавления в черный список');
            return;
        }

        // Подтверждение действия
        let message = 'Добавить в черный список:\n';
        if (fileHash) message += `- FileHash: ${fileHash.substring(0, 8)}...\n`;
        if (pubkey) message += `- Pubkey: ${pubkey.substring(0, 8)}...`;

        if (!confirm(message)) {
            return;
        }

        // Отправляем запрос на сервер
        fetch('/blacklist/add', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                file_hash: fileHash,
                pubkey: pubkey
            })
        })
        .then(response => response.json())
        .then(data => {
            if (data.success) {
                alert('Добавлено в черный список');
                // Можно обновить страницу или скрыть элемент
                // location.reload(); // Закомментировано, чтобы не было неожиданного обновления
                showSuccessMessage('Добавлено в черный список');
            } else {
                alert('Ошибка: ' + (data.error || 'Неизвестная ошибка от сервера'));
            }
        })
        .catch(error => {
            console.error('Error adding to blacklist:', error);
            alert('Ошибка добавления в черный список: ' + error.message);
        });
    }

    // Функция для копирования хеша в буфер обмена
    function copyFileHashToClipboard(fileHash) {
        if (!fileHash) {
            console.warn("FileHash is empty, nothing to copy.");
            return;
        }

        navigator.clipboard.writeText(fileHash).then(() => {
            // Визуальная обратная связь
            showSuccessMessage(`Хеш ${fileHash.substring(0, 8)}... скопирован в буфер обмена.`);
            //console.log(`FileHash ${fileHash} copied to clipboard.`);
        }).catch(err => {
            console.error('Failed to copy FileHash: ', err);
            // Попробуем альтернативный метод для старых браузеров
            const textArea = document.createElement("textarea");
            textArea.value = fileHash;
            // Избегаем прокрутки окна просмотра
            textArea.style.top = "0";
            textArea.style.left = "0";
            textArea.style.position = "fixed";
            textArea.style.opacity = "0";
            document.body.appendChild(textArea);
            textArea.focus();
            textArea.select();
            try {
                const successful = document.execCommand('copy');
                if (successful) {
                    showSuccessMessage(`Хеш ${fileHash.substring(0, 8)}... скопирован в буфер обмена (альтернативный метод).`);
                    //console.log(`FileHash ${fileHash} copied to clipboard (fallback).`);
                } else {
                    throw new Error('Copy command failed');
                }
            } catch (err2) {
                console.error('Fallback: Oops, unable to copy', err2);
                alert(`Не удалось скопировать хеш: ${fileHash}`);
            } finally {
                document.body.removeChild(textArea);
            }
        });
    }

    // Функция для перехода на страницу деталей локальной книги
    function goToLocalBook(bookID) {
        if (!bookID) {
            console.warn("Book ID is missing, cannot navigate.");
            alert("ID книги не найден.");
            return;
        }
        // URL страницы деталей книги /book/{id}
        const url = `/book/${encodeURIComponent(bookID)}`;
        //console.log(`Navigating to local book page: ${url}`);
        window.location.href = url;
    }

    // Функция для периодической проверки обновлений ответов Nostr
    // с коротким интервалом и ограниченным числом попыток
    function checkForQuickNostrResponses(maxChecks = 15, interval = 2000) {
        // --- ИЗМЕНЕНИЯ В НАЧАЛЕ ФУНКЦИИ ---
        if (isPollingForResponses) {
            console.log("Опрос ответов Nostr уже запущен, пропускаем новый запуск.");
            return; // Не запускаем новый опрос, если уже идет
        }
        isPollingForResponses = true; // Устанавливаем флаг
        console.log("Запуск опроса ответов Nostr...");
        // --- КОНЕЦ ИЗМЕНЕНИЙ ---

        let checks = 0;
        const checkInterval = setInterval(() => {
            checks++;
            console.log(`Проверка ответов Nostr, попытка ${checks}/${maxChecks}`);
            
            fetch('/request/response-count')
                .then(response => {
                    if (!response.ok) {
                        throw new Error(`HTTP error! status: ${response.status}`);
                    }
                    return response.json();
                })
                .then(data => {
                    // Проверяем, есть ли ошибка авторизации
                    if (data.error === "Unauthorized") {
                        console.log("Пользователь не авторизован, останавливаем опрос");
                        clearInterval(checkInterval);
                        isPollingForResponses = false; // Сбрасываем флаг
                        return;
                    }
                    
                    // Проверяем, есть ли ответы (count > 0)
                    if (data.count > 0) {
                        console.log(`Получены ответы на Nostr запрос! Количество: ${data.count}`);
                        clearInterval(checkInterval);
                        isPollingForResponses = false; // Сбрасываем флаг
                        
                        showSuccessMessage(`Получены ответы на запрос! Обновляем страницу...`);
                        
                        setTimeout(() => {
                            location.reload();
                        }, 1500);
                        
                    } else if (checks >= maxChecks) {
                        console.log("Максимальное количество проверок достигнуто, ответы не получены");
                        clearInterval(checkInterval);
                        isPollingForResponses = false; // Сбрасываем флаг
                        // Можно показать уведомление, что ответы не получены быстро
                        // showInfoMessage("Ответы на запрос не получены быстро. Проверьте позже.");
                    } else {
                        console.log("Ответы пока не получены, продолжаем ожидание...");
                    }
                })
                .catch(error => {
                    console.error('Ошибка при проверке ответов Nostr:', error);
                    clearInterval(checkInterval);
                    isPollingForResponses = false; // Сбрасываем флаг при ошибке
                    // Можно показать ошибку пользователю
                    // showErrorMesage("Ошибка при проверке ответов: " + error.message);
                });
        }, interval);
    }

    // Функция для проверки быстрых ответов при загрузке страницы /request,
    // если в URL есть параметр ?check_new=1
    function checkForNewResponsesOnPageLoad() {
        // Проверяем, есть ли в URL параметр ?check_new=1
        const urlParams = new URLSearchParams(window.location.search);
        const shouldCheck = urlParams.get('check_new') === '1';

        if (shouldCheck) {
            console.log("Обнаружен параметр ?check_new=1, запускаем опрос новых ответов.");

            // УДАЛЯЕМ ПАРАМЕТР ИЗ URL
            // Это важно, чтобы при F5 он не срабатывал снова.
            // Создаем новый URL без параметра check_new
            const newUrl = new URL(window.location);
            newUrl.searchParams.delete('check_new');
            // Используем replaceState, чтобы не создавать новую запись в истории
            window.history.replaceState({}, '', newUrl);
            console.log("Параметр ?check_new=1 удален из URL.");

            // Небольшая задержка перед запуском опроса, чтобы страница полностью загрузилась
            setTimeout(() => {
                // Используем существующую функцию опроса
                checkForQuickNostrResponses(15, 2000); // 15 попыток по 2 секунды = 30 секунд
            }, 1000); // 1 секунда задержки
        } else {
            console.log("Параметр ?check_new=1 не найден, опрос не запускаем.");
        }
    }

    // Назначаем обработчики после загрузки страницы
    document.addEventListener('DOMContentLoaded', function() {
        //console.log("Инициализация скриптов страницы запросов Nostr");
        
        // Назначаем обработчики для кнопок скачивания IPFS
        document.querySelectorAll('.ipfs-download-btn').forEach(button => {
            button.addEventListener('click', function(event) {
                const fileHash = this.getAttribute('data-filehash');
                const ipfsCID = this.getAttribute('data-ipfscid');
                const fileType = this.getAttribute('data-filetype');
                const title = this.getAttribute('data-title');
                downloadFromIPFS(fileHash, ipfsCID, fileType, title);
            });
        });

        // Назначаем обработчики для кнопок черного списка
        document.querySelectorAll('.blacklist-btn').forEach(button => {
            button.addEventListener('click', function() {
                const fileHash = this.getAttribute('data-filehash');
                const pubkey = this.getAttribute('data-pubkey');
                addToBlacklist(fileHash, pubkey);
            });
        });

        // Назначаем обработчики для кликов по галочкам локальных книг
        document.querySelectorAll('.local-book-link').forEach(span => {
            span.addEventListener('click', function() {
                const bookID = this.getAttribute('data-book-id');
                // Убедимся, что bookID - это число
                const bookIDInt = parseInt(bookID, 10);
                if (!isNaN(bookIDInt) && bookIDInt > 0) {
                    goToLocalBook(bookIDInt);
                } else {
                    console.warn("Invalid Book ID from data attribute:", bookID);
                    alert("Некорректный ID книги.");
                }
            });
        });
        
        // Назначаем обработчики для кликов по идентиконам (копирование хеша)
        document.querySelectorAll('.identicon-copy-btn').forEach(img => {
            img.addEventListener('click', function() {
                const fileHash = this.getAttribute('data-filehash');
                copyFileHashToClipboard(fileHash);
            });
        });

        // Обработчик отправки формы запроса
        const requestForm = document.getElementById('nostr-request-form'); // Убедитесь, что у формы есть этот ID в HTML
        if (requestForm) {
            requestForm.addEventListener('submit', function(e) {
                // Предотвращаем стандартное поведение отправки формы для показа сообщения
                // (настоящая отправка будет через fetch)
                e.preventDefault(); 
                
                // Получаем данные формы
                const formData = new FormData(this);
                
                showSuccessMessage('Отправляем запрос в сеть Nostr...');
                
                // Отправляем форму через fetch
                fetch('/request', {
                    method: 'POST',
                    body: formData,
                    cache: 'no-store'
                })
                .then(response => {
                    if (response.ok) {
                        // Успешная отправка
                        console.log("Запрос Nostr успешно отправлен");
                        showSuccessMessage('Запрос отправлен в сеть Nostr. Проверяем наличие быстрых ответов...');
                        
                        // --- ВАЖНО: Перенаправляем на /request с параметром для опроса ---
                        window.location.href = '/request?check_new=1';
                        // После этого выполнение скрипта на этой странице прекращается
                        
                    } else {
                        return response.text().then(text => {
                            throw new Error(text || `HTTP error! status: ${response.status}`);
                        });
                    }
                })
                .catch(error => {
                    console.error('Ошибка отправки запроса Nostr:', error);
                    alert('Ошибка отправки запроса: ' + (error.message || 'Неизвестная ошибка'));
                });
            });
        } else {
            console.log("Форма запроса Nostr не найдена на странице");
        }

        // Проверяем и запускаем опрос при загрузке страницы, если необходимо
        checkForNewResponsesOnPageLoad();

        //console.log("Скрипты страницы запросов Nostr инициализированы");
    });

    // Делаем некоторые функции доступными глобально, если это необходимо для обратной совместимости
    // (например, если они вызываются из inline-обработчиков, хотя их там нет)
    window.downloadFromIPFS = downloadFromIPFS;
    window.addToBlacklist = addToBlacklist;
    window.copyFileHashToClipboard = copyFileHashToClipboard;
    window.goToLocalBook = goToLocalBook;

})(); // Немедленно вызываемая функция (IIFE)