// web/static/author-scripts.js

(function() {
    'use strict';

    document.addEventListener('DOMContentLoaded', function() {
        //console.log("Инициализация скриптов страницы автора");

        const editAuthorBtn = document.getElementById('edit-author-btn');
        const titleEditForm = document.querySelector('.edit-field-form');
        const titleInput = titleEditForm ? titleEditForm.querySelector('#author-name-input') : null;
        const lastNameInput = titleEditForm ? titleEditForm.querySelector('#author-lastname-input') : null;
        const titleSaveBtn = titleEditForm ? titleEditForm.querySelector('.save-field-btn') : null;
        const displayName = document.getElementById('author-name-display'); // Предполагается, что это элемент с отображаемым именем

        if (!editAuthorBtn) {
            console.warn('Кнопка редактирования автора не найдена на странице');
            return;
        }

        // --- ПЕРЕМЕННЫЕ ДЛЯ ОТСЛЕЖИВАНИЯ СОСТОЯНИЯ ---
        let isEditing = false;
        let isLastNameManuallyChanged = false; // Флаг: было ли ручное изменение last_name
        let initialLastNameLower = ""; // Исходное значение last_name_lower из БД

        // --- Функция для извлечения последнего слова и приведения к нижнему регистру ---
        function extractAndLowerLastWord(fullName) {
            const parts = fullName.trim().split(/\s+/); // Разбиваем по пробелам
            return parts.length > 0 ? parts[parts.length - 1].toLowerCase() : fullName.toLowerCase();
        }

        // --- Функция для обновления поля last_name, если оно не изменялось вручную ---
        function updateLastNameIfNotManual() {
            //console.log("updateLastNameIfNotManual called. isLastNameManuallyChanged:", isLastNameManuallyChanged);
            if (titleInput && lastNameInput && !isLastNameManuallyChanged) {
                const fullName = titleInput.value;
                const calculatedLastNameLower = extractAndLowerLastWord(fullName);
                lastNameInput.value = calculatedLastNameLower;
                //console.log("  -> Updated last_name to:", calculatedLastNameLower);
            } else {
                //console.log("  -> Skipped update (manual change detected or inputs missing)");
            }
        }

        // --- Функция для сброса состояния перед открытием формы ---
        function resetEditState() {
            isEditing = false;
            isLastNameManuallyChanged = false; // ВАЖНО: сбросить флаг при каждом открытии
            // Получаем исходное last_name_lower из data-атрибута кнопки
            initialLastNameLower = editAuthorBtn.dataset.lastNameLower || "";
            //console.log("resetEditState: initialLastNameLower set to", initialLastNameLower);
        }
        resetEditState(); // Инициализируем состояние при загрузке

        // Показать/скрыть режим редактирования
        editAuthorBtn.addEventListener('click', function() {
            try {
                const isEditMode = this.querySelector('i').classList.contains('fa-edit');
                
                const icon = this.querySelector('i');
                if (icon.classList.contains('fa-edit')) {
                    icon.classList.remove('fa-edit');
                    icon.classList.add('fa-times');
                    this.title = 'Закрыть редактирование';
                    this.classList.add('edit-mode');
                    if (displayName) displayName.style.display = 'none';
                    if (titleEditForm) titleEditForm.style.display = 'flex';
                    
                    // --- ИНИЦИАЛИЗИРУЕМ ПОЛЯ ВВОДА ---
                    if (titleInput && displayName) {
                        const fullNameValue = displayName.textContent || "";
                        titleInput.value = fullNameValue;
                        //console.log("Initialized full_name input to:", fullNameValue);
                    }
                    if (lastNameInput) {
                        lastNameInput.value = initialLastNameLower; // Используем значение из БД
                        //console.log("Initialized last_name input to:", initialLastNameLower);
                    }
                    
                    if (titleInput) {
                        titleInput.focus();
                        titleInput.select();
                    }
                    
                    // --- СБРАСЫВАЕМ ФЛАГ РУЧНОГО ИЗМЕНЕНИЯ ---
                    isLastNameManuallyChanged = false; // ВАЖНО: сбросить флаг при открытии
                    // --- УСТАНАВЛИВАЕМ ФЛАГ РЕДАКТИРОВАНИЯ ---
                    isEditing = true;
                } else {
                    icon.classList.remove('fa-times');
                    icon.classList.add('fa-edit');
                    this.title = 'Редактировать автора';
                    this.classList.remove('edit-mode');
                    if (displayName) displayName.style.display = 'block';
                    if (titleEditForm) titleEditForm.style.display = 'none';
                    // --- СБРАСЫВАЕМ ФЛАГ РЕДАКТИРОВАНИЯ ---
                    isEditing = false;
                }
            } catch (e) {
                console.error('Ошибка при переключении режима редактирования:', e);
            }
        });

        // --- Функция сохранения ---
        function saveAuthorName(fullNameValue, lastNameLowerValue) {
            const authorIdElement = document.querySelector('[data-author-id]'); // Ищем элемент с data-author-id
            const authorId = authorIdElement ? authorIdElement.dataset.authorId : null;
            
            if (!authorId) {
                console.error('ID автора не найден');
                alert('Ошибка: ID автора не найден');
                return;
            }

            if (!fullNameValue.trim()) {
                alert('Имя автора не может быть пустым');
                return;
            }

            // --- ПОДГОТОВКА ДАННЫХ ФОРМЫ ---
            const formData = new FormData();
            formData.append('name', fullNameValue); // full_name
            // Отправляем введенное значение last_name_lower, оно будет приведено к нижнему регистру на сервере
            formData.append('last_name_lower', lastNameLowerValue); 

            fetch(`/save/author/${authorId}`, {
                method: 'POST',
                body: formData
            })
            .then(response => {
                 if (response.ok) {
                     return response.text().then(text => {
                         // Ожидаем формат "OK:<ID>" или просто "OK:<ID>" если сервер так отвечает
                         const parts = text.split(':');
                         if ((parts.length === 2 && parts[0] === "OK") || (parts.length === 1 && parts[0].startsWith("OK"))) {
                             const savedId = parts.length === 2 ? parts[1] : parts[0].substring(2); // Извлекаем ID
                             // Обновляем отображаемое имя
                             if (displayName) displayName.textContent = fullNameValue;
                             const pageTitle = document.querySelector('title');
                             if (pageTitle) {
                                 pageTitle.textContent = fullNameValue + ' - Turanga';
                             }
                             // Закрываем форму редактирования
                             cancelAuthorEdit(); 
                             return { success: true, id: savedId };
                         } else {
                             // Сервер вернул что-то неожиданное
                             throw new Error(text || `Неожиданный ответ сервера`);
                         }
                     });
                 } else {
                     return response.text().then(text => {
                         throw new Error(text || `Ошибка сервера: ${response.status}`);
                     });
                 }
            })
            .then(data => {
                 if (data && data.success) {
                     //console.log("Имя автора успешно сохранено, ID:", data.id);
                 }
            })
            .catch(error => {
                console.error('Ошибка сохранения имени автора:', error);
                alert('Ошибка сохранения: ' + (error.message || 'Неизвестная сетевая ошибка'));
            });
        }

        // --- Обработчик кнопки сохранения ---
        if (titleSaveBtn) {
            titleSaveBtn.addEventListener('click', function() {
                if (titleInput && lastNameInput) {
                    // Перед сохранением убеждаемся, что last_name актуален (если не было ручного изменения)
                    // updateLastNameIfNotManual(); // Не нужно, так как blur уже сработал
                    saveAuthorName(titleInput.value.trim(), lastNameInput.value.trim()); // Отправляем введённое значение
                }
            });
        }

        // --- Функция отмены редактирования ---
        function cancelAuthorEdit() {
            if (displayName) displayName.style.display = 'block';
            if (titleEditForm) titleEditForm.style.display = 'none';
            const icon = editAuthorBtn.querySelector('i');
            if (icon && icon.classList.contains('fa-times')) {
                icon.classList.remove('fa-times');
                icon.classList.add('fa-edit');
                editAuthorBtn.title = 'Редактировать автора';
                editAuthorBtn.classList.remove('edit-mode');
            }
            // --- СБРАСЫВАЕМ СОСТОЯНИЕ ---
            resetEditState();
        }

        // --- Обработчики для полей ввода ---
        if (titleInput) {
            // Сохранение по Enter
            titleInput.addEventListener('keydown', function(e) {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    if (lastNameInput) {
                         // updateLastNameIfNotManual(); // Не нужно, так как blur уже сработал
                         saveAuthorName(this.value.trim(), lastNameInput.value.trim()); // Отправляем введённое значение
                    }
                } else if (e.key === 'Escape') {
                    e.preventDefault();
                    cancelAuthorEdit();
                }
            });

            // --- Автообновление last_name при потере фокуса с full_name ---
            titleInput.addEventListener('blur', function() {
                //console.log("Full name input lost focus");
                updateLastNameIfNotManual(); // Вызываем обновление при потере фокуса
            });
            
            // --- Сброс флага при изменении full_name ---
            // Это нужно, чтобы если пользователь ввел что-то в last_name, потом изменил full_name,
            // и снова начал вводить в last_name, система считала его изменённым.
            titleInput.addEventListener('input', function() {
                 // Не сбрасываем isLastNameManuallyChanged здесь, так как мы хотим,
                 // чтобы он сбрасывался только при открытии формы или ручном изменении last_name.
                 // Однако, если пользователь вручную изменил last_name, а потом изменил full_name,
                 // и потом снова начал вводить в last_name, это всё равно будет считаться ручным изменением.
            });
        }

        if (lastNameInput) {
            // Сохранение по Enter в поле last_name
            lastNameInput.addEventListener('keydown', function(e) {
                 if (e.key === 'Enter') {
                    e.preventDefault();
                    if (titleInput) {
                         // updateLastNameIfNotManual(); // Не нужно, так как blur уже сработал
                         saveAuthorName(titleInput.value.trim(), this.value.trim()); // Отправляем введённое значение
                    }
                 } else if (e.key === 'Escape') {
                    e.preventDefault();
                    cancelAuthorEdit();
                 }
            });

            // --- Отслеживание ручного изменения поля last_name ---
            lastNameInput.addEventListener('input', function() {
                //console.log("Last name input changed by user");
                isLastNameManuallyChanged = true; // Пользователь начал вводить, считаем изменённым
                //console.log("  -> isLastNameManuallyChanged set to true");
            });
            
            // Потеря фокуса с поля last_name
            lastNameInput.addEventListener('blur', function() {
                // Можно добавить валидацию или нормализацию введённого значения здесь, если нужно
                // Например, привести к нижнему регистру сразу при потере фокуса
                // this.value = this.value.toLowerCase();
                //console.log("Last name input lost focus, value normalized to lowercase");
            });
        }

        // --- Глобальная обработка ESC ---
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && isEditing) {
                e.preventDefault();
                //console.log("Нажат Esc, выходим из режима редактирования автора");
                cancelAuthorEdit();
            }
        });

        //console.log("Скрипты страницы автора инициализированы");
    });

})();
