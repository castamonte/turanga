// web/static/scripts.js

// Функция для отображения сообщений
function showMessage(text, type, container) {
    if (container) {
        container.textContent = text;
        container.className = 'message';
        if (type === 'error') {
            container.classList.add('error-message');
        } else if (type === 'success') {
            container.classList.add('success-message');
        } else if (type === 'warning') {
            container.classList.add('warning-message');
        }
        container.style.display = 'block';
    }
}

// --- Код для модального окна авторизации ---
function initAuthModal() {
    const authOverlay = document.getElementById('auth-overlay');
    // КРИТИЧНО: Проверяем существование элементов модального окна авторизации
    if (!authOverlay || !document.getElementById('auth-modal-form')) return;

    const authForm = document.getElementById('auth-modal-form');
    const authCancelBtn = document.getElementById('auth-cancel-btn');
    const passwordInput = document.getElementById('modal-password');
    const errorMessage = document.getElementById('auth-error-message');
    
    if (!authOverlay) return; // Если элементов нет, выходим

    // Функция для открытия модального окна
    function openAuthModal() {
        authOverlay.style.display = 'block';
        // Устанавливаем фокус на поле ввода пароля
        if (passwordInput) {
            setTimeout(() => passwordInput.focus(), 100);
        }
    }
    
    // Функция для закрытия модального окна
    function closeAuthModal() {
        authOverlay.style.display = 'none';
        // Очищаем форму и сообщения об ошибках
        if (authForm) authForm.reset();
        if (errorMessage) {
            errorMessage.style.display = 'none';
            errorMessage.textContent = '';
        }
    }
    
    // Проверяем, есть ли кнопка "Авторизация" (admin-link) в header
    const authLink = document.querySelector('.header-actions .admin-link[href="/auth"]');
    if (authLink) {
        // Заменяем стандартный переход на открытие модального окна
        authLink.addEventListener('click', function(e) {
            e.preventDefault();
            openAuthModal();
        });
    }
    
    // Обработчик отправки формы в модальном окне
    if (authForm) {
        authForm.addEventListener('submit', function(e) {
            e.preventDefault();
            
            const password = passwordInput ? passwordInput.value : '';
            if (!password) {
                if (errorMessage) {
                    errorMessage.textContent = 'Пожалуйста, введите пароль.';
                    errorMessage.style.display = 'block';
                    errorMessage.style.backgroundColor = '#f8d7da';
                    errorMessage.style.color = '#721c24';
                    errorMessage.style.border = '1px solid #f5c6cb';
                }
                return;
            }
            
            // Отправляем данные через fetch
            const formData = new FormData();
            formData.append('password', password);
            
            fetch('/auth', {
                method: 'POST',
                body: formData
            })
            .then(response => {
                if (response.ok) {
                    window.location.href = '/';
                } else {
                    return response.text().then(text => { throw new Error(text); });
                }
            })
            .catch(error => {
                console.error('Ошибка авторизации:', error);
                if (errorMessage) {
                    const errorMsg = error.message && error.message !== 'Unauthorized' ? error.message : 'Неверный пароль.';
                    errorMessage.textContent = errorMsg;
                    errorMessage.style.display = 'block';
                    errorMessage.style.backgroundColor = '#f8d7da';
                    errorMessage.style.color = '#721c24';
                    errorMessage.style.border = '1px solid #f5c6cb';
                }
                if (passwordInput) passwordInput.value = '';
            });
        });
    }
    
    // Обработчик кнопки "Отмена"
    if (authCancelBtn) {
        authCancelBtn.addEventListener('click', closeAuthModal);
    }
    
    // Закрытие модального окна при клике вне его области
    authOverlay.addEventListener('click', function(e) {
        if (e.target === authOverlay) {
            closeAuthModal();
        }
    });
    
    // Закрытие модального окна по клавише Escape
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && authOverlay.style.display === 'block') {
            closeAuthModal();
        }
    });
}

// --- Код для загрузки книги ---
function initQuickUploadModal() {
    const quickUploadOverlay = document.getElementById('quick-upload-overlay');
    // Проверяем только наличие обертки
    if (!quickUploadOverlay) return;

    // Проверяем, есть ли кнопка "Добавить книгу" (admin-link) в header
    const uploadLink = document.querySelector('.header-actions .admin-link[href="/upload"]');
    if (uploadLink) {
        uploadLink.addEventListener('click', function(e) {
            if (e.shiftKey || e.ctrlKey || e.metaKey) {
                return;
            }
            e.preventDefault();
            
            // Просто показываем уже существующее модальное окно
            quickUploadOverlay.style.display = 'flex';
            
            // Инициализируем обработчики формы (если еще не инициализированы)
            initUploadModalForm();
        });
    }

    // Закрытие модального окна по клику вне области модального окна
    quickUploadOverlay.addEventListener('click', function(e) {
        if (e.target === quickUploadOverlay) {
            quickUploadOverlay.style.display = 'none';
        }
    });

    // Закрытие модального окна по клавише Escape
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && quickUploadOverlay && quickUploadOverlay.style.display !== 'none') {
            quickUploadOverlay.style.display = 'none';
        }
    });
}

// Функция для инициализации формы загрузки внутри модального окна
function initUploadModalForm() {
    const uploadModalForm = document.getElementById('upload-modal-form');
    const uploadModalCancelBtn = document.getElementById('upload-modal-cancel-btn');
    const modalFileInput = document.getElementById('modal-book-file');
    const modalMessageDiv = document.getElementById('upload-modal-message');
    const modalProgressDiv = document.getElementById('upload-modal-progress');
    const uploadProgressInfo = document.getElementById('upload-progress-info');

    if (!uploadModalForm) {
        console.error("Форма модального окна загрузки не найдена!");
        return;
    }

    // Предотвращаем повторную инициализацию
    if (uploadModalForm.dataset.initialized === 'true') {
        return;
    }
    uploadModalForm.dataset.initialized = 'true';

    uploadModalForm.addEventListener('submit', function(e) {
        e.preventDefault();

        const files = modalFileInput ? modalFileInput.files : null;
        if (!files || files.length === 0) {
            showMessage('Пожалуйста, выберите файлы для загрузки.', 'error', modalMessageDiv);
            return;
        }

        if (modalMessageDiv) modalMessageDiv.style.display = 'none';
        if (modalProgressDiv) modalProgressDiv.style.display = 'block';
        if (uploadProgressInfo) uploadProgressInfo.innerHTML = '';

        // Создаем контейнер для отслеживания прогресса
        const totalFiles = files.length;
        let completedFiles = 0;
        let failedFiles = 0;

        // Функция для обновления прогресса
        function updateProgress() {
            if (uploadProgressInfo) {
                uploadProgressInfo.innerHTML = `
                    <div style="margin-top: 10px; font-size: 14px;">
                        Обработано: ${completedFiles + failedFiles} из ${totalFiles}
                        ${failedFiles > 0 ? `<br><span style="color: #dc3545;">Ошибок: ${failedFiles}</span>` : ''}
                    </div>
                `;
            }
        }

        updateProgress();

        // Обрабатываем каждый файл по отдельности
        const promises = [];
        for (let i = 0; i < files.length; i++) {
            const file = files[i];
            const formData = new FormData();
            formData.append('book_file', file); 

            const promise = fetch('/upload', {
                method: 'POST',
                body: formData
            })
            .then(response => {
                completedFiles++;
                updateProgress();
                if (!response.ok) {
                    failedFiles++;
                    updateProgress();
                    return response.text().then(text => { 
                        throw new Error(text || `Ошибка загрузки файла ${file.name}`); 
                    });
                }
                return response.text();
            })
            .catch(error => {
                failedFiles++;
                updateProgress();
                console.error(`Ошибка загрузки файла ${file.name}:`, error);
                throw error;
            });

            promises.push(promise);
        }

        // Ждем завершения всех загрузок
        Promise.allSettled(promises)
            .then(results => {
                if (modalProgressDiv) modalProgressDiv.style.display = 'none';
                
                const successful = results.filter(result => result.status === 'fulfilled').length;
                const failed = results.filter(result => result.status === 'rejected').length;
                
                if (failed === 0) {
                    showMessage(`Успешно загружено книг: ${successful}`, 'success', modalMessageDiv);
                } else {
                    showMessage(`Загружено: ${successful}, Ошибок: ${failed}`, 'warning', modalMessageDiv);
                }
                
                // Сброс формы
                uploadModalForm.reset();
                
                // Закрываем модальное окно и перезагружаем страницу
                setTimeout(() => {
                    const quickUploadOverlay = document.getElementById('quick-upload-overlay');
                    if (quickUploadOverlay) quickUploadOverlay.style.display = 'none';
                    window.location.reload();
                }, 1000);
            });
    });

    // Обработчик кнопки "Отмена" в модальном окне загрузки
    if (uploadModalCancelBtn) {
        uploadModalCancelBtn.addEventListener('click', function() {
            const quickUploadOverlay = document.getElementById('quick-upload-overlay');
            if (quickUploadOverlay) quickUploadOverlay.style.display = 'none';
        });
    } else {
        console.warn("Кнопка 'Отмена' в модальном окне загрузки не найдена");
    }
}

// --- Код для модального окна ревизии ---
function initRevisionModal() {
    const revisionOverlay = document.getElementById('revision-overlay');
    // Проверяем только наличие обертки
    if (!revisionOverlay) {
        console.log("Модальное окно ревизии не найдено на странице");
        return;
    }

    // Проверяем, есть ли кнопка "Ревизия" (admin-link) в header
    const revisionLink = document.querySelector('.header-actions .admin-link[href="/revision"]');
    if (revisionLink) {
        revisionLink.addEventListener('click', function(e) {
            e.preventDefault();
            //console.log("Клик по ссылке ревизии");
            
            // Показываем модальное окно (оверлей)
            revisionOverlay.style.display = 'flex';
            //console.log("Модальное окно ревизии показано (display=flex)");
            
            // Инициализируем обработчики формы внутри модального окна
            initRevisionModalForm();
        });
    } else {
        console.log("Ссылка на ревизию не найдена");
    }

    // Закрытие модального окна по клавише Escape
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && revisionOverlay && revisionOverlay.style.display !== 'none') {
            //console.log("Нажата клавиша Escape, закрываем модальное окно ревизии");
            revisionOverlay.style.display = 'none';
        }
    });
}

// Функция для инициализации формы ревизии внутри модального окна
function initRevisionModalForm() {
    const revisionForm = document.getElementById('revision-form');
    const revisionCancelBtn = document.getElementById('revision-cancel');
    const revisionMessageDiv = document.getElementById('revision-message');
    const revisionProgressDiv = document.getElementById('revision-progress');
    const revisionProgressBar = document.getElementById('revision-progress-bar');
    const revisionProgressText = document.getElementById('revision-progress-text');
    const revisionOverlay = document.getElementById('revision-overlay');

    //console.log("Инициализация формы модального окна ревизии:");
    //console.log("  revisionForm:", revisionForm);
    //console.log("  revisionCancelBtn:", revisionCancelBtn);

    // Функция для показа сообщения внутри модального окна
    function showMessageInModal(text, type, container) {
        // Если контейнер сообщений не передан, ищем стандартный
        const msgContainer = container || revisionMessageDiv;
        if (msgContainer) {
            msgContainer.textContent = text;
            msgContainer.className = 'message';
            if (type === 'error') {
                msgContainer.classList.add('error-message');
            } else if (type === 'success') {
                msgContainer.classList.add('success-message');
            } else if (type === 'warning') {
                msgContainer.classList.add('warning-message');
            }
            msgContainer.style.display = 'block';
        }
    }

    if (!revisionForm) {
        console.error("Форма модального окна ревизии не найдена!");
        showMessageInModal("Не удалось инициализировать форму ревизии. Пожалуйста, перезагрузите страницу.", 'error', revisionMessageDiv);
        return;
    }

    // Функция для сброса прогресс-бара
    function resetProgressBar() {
        if (revisionProgressBar) revisionProgressBar.style.width = '0%';
        if (revisionProgressText) revisionProgressText.textContent = 'Подготовка...';
    }

    // Функция для обновления прогресс-бара
    function updateProgressBar(percent, text) {
        if (revisionProgressBar) {
            const clampedPercent = Math.max(0, Math.min(100, percent));
            revisionProgressBar.style.width = clampedPercent + '%';
        }
        if (revisionProgressText) revisionProgressText.textContent = text;
    }

    // Удаляем предыдущие обработчики, чтобы избежать дублирования
    const newRevisionForm = revisionForm.cloneNode(true);
    revisionForm.parentNode.replaceChild(newRevisionForm, revisionForm);
    const freshRevisionForm = document.getElementById('revision-form');

    freshRevisionForm.addEventListener('submit', function(e) {
        e.preventDefault();
        //console.log("Форма ревизии отправлена");
        
        // Скрываем форму и показываем прогресс
        if (freshRevisionForm) freshRevisionForm.style.display = 'none';
        if (revisionProgressDiv) {
            revisionProgressDiv.style.display = 'block';
            //console.log("Прогресс ревизии показан");
        }
        if (revisionMessageDiv) revisionMessageDiv.style.display = 'none';
        
        resetProgressBar();
        updateProgressBar(0, 'Отправка запроса на сервер...');
        
        // Отправляем запрос на ревизию
        //console.log("Отправка запроса на ревизию...");
        fetch('/revision', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
        })
        .then(response => {
            //console.log("Ответ от сервера:", response.status, response.statusText);
            if (response.ok) {
                // Сервер принял запрос на ревизию
                console.log("Ревизия начата успешно");
                updateProgressBar(5, 'Ревизия начата на сервере...');
                // Запускаем опрос сервера о статусе
                pollRevisionStatus();
                return response.json();
            } else {
                // Сервер вернул ошибку
                console.error("Ошибка от сервера при запуске ревизии:", response.status);
                return response.text().then(text => {
                    // Пытаемся получить текст ошибки
                    const errorMsg = text || response.statusText || `Ошибка ${response.status}`;
                    throw new Error(errorMsg);
                });
            }
        })
        .catch(error => {
            console.error('Ошибка ревизии (сетевая или логическая):', error);
            // Останавливаем отображение прогресса
            if (revisionProgressDiv) revisionProgressDiv.style.display = 'none';
            // Показываем форму обратно, чтобы пользователь мог попробовать снова
            if (freshRevisionForm) freshRevisionForm.style.display = 'block';
            // Показываем сообщение об ошибке в модальном окне
            showMessageInModal(`Ошибка при запуске ревизии: ${error.message || 'Неизвестная ошибка'}`, 'error', revisionMessageDiv);
        });
    });

    // Обработчик кнопки "Отмена" в модальном окне ревизии
    if (revisionCancelBtn) {
        // Удаляем предыдущие обработчики
        const newCancelBtn = revisionCancelBtn.cloneNode(true);
        revisionCancelBtn.parentNode.replaceChild(newCancelBtn, revisionCancelBtn);
        document.getElementById('revision-cancel').addEventListener('click', function() {
            //console.log("Клик по кнопке 'Отмена' в модальном окне ревизии");
            if (revisionOverlay) revisionOverlay.style.display = 'none';
        });
    } else {
        console.warn("Кнопка 'Отмена' в модальном окне ревизии не найдена");
    }

    // Закрытие модального окна при клике вне области модального окна
    if (revisionOverlay) {
        revisionOverlay.addEventListener('click', function(e) {
            if (e.target === revisionOverlay) {
                //console.log("Клик вне модального окна ревизии, закрываем");
                revisionOverlay.style.display = 'none';
            }
        });
    }
    
    //console.log("Обработчики формы ревизии инициализированы");
}

// Функция для опроса сервера о статусе ревизии
function pollRevisionStatus() {
    //console.log("Начинаем опрос сервера о статусе ревизии");
    
    const interval = setInterval(() => {
        fetch('/revision/progress')
            .then(response => {
                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                return response.json();
            })
            .then(data => {
                //console.log("Получен статус ревизии:", data);
                
                // Получаем элементы внутри функции для актуальных ссылок
                const revisionProgressBar = document.getElementById('revision-progress-bar');
                const revisionProgressText = document.getElementById('revision-progress-text');
                const revisionMessageDiv = document.getElementById('revision-message');
                const revisionProgressDiv = document.getElementById('revision-progress');
                const revisionOverlay = document.getElementById('revision-overlay');
                
                if (revisionProgressBar) {
                    revisionProgressBar.style.width = data.progress + '%';
                }
                if (revisionProgressText) {
                    revisionProgressText.textContent = data.message;
                }
                
                // Проверяем статус
                if (data.status === "completed") {
                    clearInterval(interval);
                    console.log("Ревизия завершена");
                    
                    // Показываем сообщение об успехе
                    if (revisionMessageDiv) {
                        revisionMessageDiv.textContent = 'Ревизия успешно завершена!';
                        revisionMessageDiv.className = 'message success-message';
                        revisionMessageDiv.style.display = 'block';
                    }
                    
                    if (revisionProgressDiv) {
                        revisionProgressDiv.style.display = 'none';
                    }
                    
                    // Закрываем модальное окно через 2 секунды
                    setTimeout(() => {
                        //console.log("Закрываем модальное окно ревизии");
                        if (revisionOverlay) {
                            revisionOverlay.style.display = 'none';
                        }
                        // Показываем уведомление об успешной ревизии
                        //showMessage('Ревизия успешно завершена!', 'success', document.querySelector('.header'));
                    }, 1000);

                } else if (data.status === "error") {
                    clearInterval(interval);
                    console.error("Ошибка ревизии:", data.error);
                    
                    if (revisionMessageDiv) {
                        revisionMessageDiv.textContent = `Ошибка ревизии: ${data.error}`;
                        revisionMessageDiv.className = 'message error-message';
                        revisionMessageDiv.style.display = 'block';
                    }
                    
                    if (revisionProgressDiv) {
                        revisionProgressDiv.style.display = 'none';
                    }
                    
                    // Показываем форму обратно
                    const revisionForm = document.getElementById('revision-form');
                    if (revisionForm) revisionForm.style.display = 'block';
                }
            })
            .catch(error => {
                console.error('Ошибка при получении статуса ревизии:', error);
                clearInterval(interval);
            });
    }, 1000);
}

// Инициализация при загрузке DOM
document.addEventListener('DOMContentLoaded', function() {
    // Инициализация при загрузке DOM
    initAuthModal();
    initQuickUploadModal();
    initRevisionModal();
});