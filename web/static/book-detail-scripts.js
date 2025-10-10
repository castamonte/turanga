// web/static/book-detail-scripts.js

// Обернем код в функцию, чтобы избежать глобальных переменных
(function() {
    'use strict';

    // Универсальная функция копирования в буфер обмена
    function copyToClipboard(text) {
        // Проверяем, доступен ли современный API и мы в безопасном контексте
        if (navigator.clipboard && window.isSecureContext) {
            return navigator.clipboard.writeText(text).catch(function(err) {
                console.error('Ошибка clipboard API:', err);
                // Если clipboard API не работает, используем fallback
                return fallbackCopyTextToClipboard(text);
            });
        } else {
            // Используем fallback для HTTP или старых браузеров
            return fallbackCopyTextToClipboard(text);
        }
    }

    // Fallback метод для копирования текста
    function fallbackCopyTextToClipboard(text) {
        return new Promise(function(resolve, reject) {
            const textArea = document.createElement("textarea");
            textArea.value = text;
            
            // Стили для скрытия textarea
            textArea.style.position = "fixed";
            textArea.style.top = "0";
            textArea.style.left = "0";
            textArea.style.width = "2em";
            textArea.style.height = "2em";
            textArea.style.padding = "0";
            textArea.style.border = "none";
            textArea.style.outline = "none";
            textArea.style.boxShadow = "none";
            textArea.style.background = "transparent";
            textArea.style.opacity = "0";
            
            document.body.appendChild(textArea);
            textArea.focus();
            textArea.select();
            
            try {
                const successful = document.execCommand('copy');
                document.body.removeChild(textArea);
                if (successful) {
                    resolve();
                } else {
                    reject(new Error('Не удалось выполнить копирование'));
                }
            } catch (err) {
                document.body.removeChild(textArea);
                reject(err);
            }
        });
    }

    // Функция для загрузки обложки на сервер
    function uploadCover(file, fileHash, bookID) {
        // Простая проверка типа файла (можно усилить на сервере)
        if (!file.type.startsWith('image/')) {
            alert('Пожалуйста, выберите файл изображения.');
            return;
        }
        const formData = new FormData();
        formData.append('cover', file);
        formData.append('file_hash', fileHash); // Передаем хеш для именования
        
        // Показываем индикатор загрузки (можно улучшить)
        const coverElement = document.querySelector('.book-cover');
        if (!coverElement) {
            console.error('Элемент обложки не найден');
            return;
        }
        const originalContent = coverElement.innerHTML;
        coverElement.innerHTML = '<i class="fas fa-spinner fa-spin" style="font-size: 48px;"></i>';
        
        fetch(`/save/book/${encodeURIComponent(bookID)}/cover`, { // Новый эндпоинт
            method: 'POST',
            body: formData
        })
        .then(response => {
            if (!response.ok) {
                return response.text().then(text => { 
                    throw new Error(text || `HTTP error! status: ${response.status}`); 
                });
            }
            // Проверяем Content-Type ответа
            const contentType = response.headers.get("content-type");
            if (contentType && contentType.indexOf("application/json") !== -1) {
                return response.json(); // Ожидаем JSON с новым URL обложки
            } else {
                // Если не JSON, пробуем получить текст для отладки
                return response.text().then(text => {
                    console.error("Ответ сервера не является JSON:", text);
                    throw new Error(`Сервер вернул неожиданный ответ: ${response.status} ${response.statusText}`);
                });
            }
        })
        .then(data => {
            if (data.success && data.cover_url) {
                // Обновляем изображение обложки на странице
                const imgElement = document.querySelector('.book-cover img');
                const placeholderElement = document.querySelector('.book-cover-placeholder');
                if (imgElement) {
                    // Если изображение уже есть, просто обновляем src
                    // Добавляем timestamp для обхода кэша
                    imgElement.src = data.cover_url + '?t=' + new Date().getTime();
                } else if (placeholderElement) {
                    // Если был placeholder, заменяем его на изображение
                    const newImg = document.createElement('img');
                    newImg.src = data.cover_url + '?t=' + new Date().getTime();
                    newImg.alt = "Обложка";
                    placeholderElement.parentNode.replaceChild(newImg, placeholderElement);
                }
                // Сбрасываем input, чтобы можно было выбрать тот же файл снова
                const coverUploadInput = document.getElementById('cover-upload-input');
                if (coverUploadInput) {
                    coverUploadInput.value = '';
                }
                alert('Обложка успешно обновлена!');
            } else {
                throw new Error(data.message || 'Неизвестная ошибка от сервера');
            }
        })
        .catch(error => {
            console.error('Ошибка загрузки обложки:', error);
            alert('Ошибка загрузки обложки: ' + (error.message || 'Неизвестная ошибка'));
            // Восстанавливаем оригинальное содержимое обложки
            coverElement.innerHTML = originalContent;
        });
        setTimeout(() => location.reload(), 200);
    }

    // Сохранение изменений названия
    function saveTitleChange(value, bookID) {
        const formData = new FormData();
        formData.append('field', 'title');
        formData.append('value', value);
        
        fetch(`/save/book/${encodeURIComponent(bookID)}`, {
            method: 'POST',
            body: formData
        })
        .then(response => {
            if (response.ok) {
                // Обновляем отображение названия
                const titleElement = document.querySelector('.header h1');
                if (titleElement) {
                    titleElement.textContent = value || 'Без названия';
                }
                document.title = (value || 'Без названия') + ' - Turanga';
                // Обновляем data-value для поля заголовка
                const titleField = document.querySelector('.editable-field[data-field="title"]');
                if (titleField) {
                    titleField.dataset.value = value;
                }
                // Возвращаемся к отображению заголовка
                cancelTitleEdit();
            } else {
                return response.text().then(text => {
                    throw new Error(text || `HTTP error! status: ${response.status}`);
                });
            }
        })
        .catch(error => {
            console.error('Ошибка сохранения названия:', error);
            alert('Ошибка сохранения названия: ' + (error.message || 'Неизвестная ошибка'));
        });
    }            

    // Отмена редактирования названия
    function cancelTitleEdit() {
        // Показываем заголовок и скрываем форму
        const titleElement = document.querySelector('.header h1');
        if (titleElement) titleElement.style.display = 'block';
        
        const titleEditForm = document.querySelector('.edit-field-form');
        if (titleEditForm) titleEditForm.style.display = 'none';
        
        // Показываем кнопки, если мы в режиме редактирования
        const editBookBtn = document.getElementById('edit-book-btn');
        const isEditMode = editBookBtn && editBookBtn.querySelector('i').classList.contains('fa-times');
        
        const titleEditBtn = document.querySelector('.header .edit-field-btn');
        if (isEditMode) {
            if (titleEditBtn) titleEditBtn.style.display = 'inline-flex';
            const titleDeleteBtn = document.getElementById('delete-book-btn');
            if (titleDeleteBtn) titleDeleteBtn.style.display = 'inline-flex';
        }
    }

    // Сохранение изменений поля
    function saveFieldChanges(field, bookID) {
        const fieldName = field.dataset.field;
        const inputs = field.querySelectorAll('.edit-field-input');
        const textarea = field.querySelector('.edit-field-textarea');
        const checkbox = field.querySelector('.edit-field-checkbox');
        let value = '';
        
        if (inputs.length > 0) {
            if (fieldName === 'series') {
                // Для серии у нас два поля: название и номер
                const seriesName = inputs[0].value.trim();
                // Ищем поле номера серии по дополнительному классу
                const seriesNumberInput = field.querySelector('.edit-field-input-series-number');
                const seriesNumber = seriesNumberInput ? seriesNumberInput.value.trim() : '';
                value = seriesName + '|' + seriesNumber; // Разделяем через |
            } else {
                value = inputs[0].value.trim();
            }
        } else if (textarea) {
            value = textarea.value.trim();
        } else if (checkbox) {
            value = checkbox.checked ? 'true' : 'false';
        }

        // Отправляем изменения через fetch
        const formData = new FormData();
        formData.append('field', fieldName);
        formData.append('value', value);
        
        fetch(`/save/book/${encodeURIComponent(bookID)}`, {
            method: 'POST',
            body: formData
        })
        .then(response => {
            if (response.ok) {
                // Обновляем отображение поля
                updateFieldDisplay(field, value, fieldName);
                cancelFieldEdit(field);
            } else {
                return response.text().then(text => {
                    throw new Error(text || `HTTP error! status: ${response.status}`);
                });
            }
        })
        .catch(error => {
            console.error(`Ошибка сохранения поля ${fieldName}:`, error);
            alert(`Ошибка сохранения поля ${fieldName}: ` + (error.message || 'Неизвестная ошибка'));
        });
    }

    // Обновление отображения поля
    function updateFieldDisplay(field, value, fieldName) {
        const display = field.querySelector('.editable-display');
        const placeholder = field.querySelector('.empty-placeholder');
        if (!display) return;
        
        // Проверяем, включен ли режим редактирования
        const editBookBtn = document.getElementById('edit-book-btn');
        const isEditMode = editBookBtn && editBookBtn.querySelector('i').classList.contains('fa-times');
        
        // Обновляем data-value атрибут поля
        field.dataset.value = value;
        
        if (fieldName === 'series') {
            // Разбираем значение серии
            const parts = value.split('|');
            const seriesName = parts[0] || '';
            const seriesNumber = parts[1] || '';
            if (display) {
                if (seriesName) {
                    // Формируем HTML с ссылкой на серию
                    let seriesHTML = '<a href="/s/' + encodeURIComponent(seriesName) + '">' + seriesName + '</a>';
                    if (seriesNumber) {
                        seriesHTML += ' #' + seriesNumber;
                    }
                    display.innerHTML = seriesHTML;
                    display.style.display = 'inline';
                } else {
                    display.textContent = 'Не указана';
                    display.style.display = 'inline';
                }
            }
            if (placeholder) {
                placeholder.style.display = !seriesName && isEditMode ? 'inline' : 'none';
            }
        } else if (fieldName === 'over18') {
            const isChecked = value === 'true';
            const label = field.querySelector('.over18-label');
            if (label) {
                label.style.display = isChecked ? 'inline' : (isEditMode ? 'inline' : 'none');
            }
            if (isChecked) {
                display.innerHTML = '<span class="over18-badge">18+</span>';
            } else {
                display.innerHTML = '';
            }
        } else if (fieldName === 'authors') {
            display.textContent = value || 'Не указаны';
            display.style.display = 'inline';
        } else if (fieldName === 'annotation') {
            const p = display.querySelector('p');
            if (p) {
                p.textContent = value || '';
            }
            display.style.display = 'block';
        } else if (fieldName === 'tags') {
            // Для тегов ничего не делаем при сохранении, так как они обновляются по-другому
        } else {
            // Для остальных полей
            if (value) {
                display.textContent = value;
                display.style.display = 'inline';
            } else {
                display.textContent = 'Не указан';
                display.style.display = 'inline';
            }
        }
    }

    // Отмена редактирования поля
    function cancelFieldEdit(field) {
        const display = field.querySelector('.editable-display');
        const placeholder = field.querySelector('.empty-placeholder');
        const editBtn = field.querySelector('.edit-field-btn');
        const form = field.querySelector('.edit-field-form');
        
        // Проверяем, включен ли режим редактирования
        const editBookBtn = document.getElementById('edit-book-btn');
        const isEditMode = editBookBtn && editBookBtn.querySelector('i').classList.contains('fa-times');
        
        if (display) {
            const fieldName = field.dataset.field;
            const currentValue = field.dataset.value;
            
            if (fieldName === 'over18') {
                const isChecked = currentValue === 'true';
                const label = field.querySelector('.over18-label');
                if (label) {
                    label.style.display = isChecked ? 'inline' : (isEditMode ? 'inline' : 'none');
                }
                if (isChecked) {
                    display.innerHTML = '<span class="over18-badge">18+</span>';
                } else {
                    display.innerHTML = '';
                }
            } else if (fieldName === 'authors') {
                display.textContent = currentValue || 'Не указаны';
                display.style.display = 'inline';
            } else if (fieldName === 'series') {
                const parts = currentValue.split('|');
                const seriesName = parts[0] || '';
                const seriesNumber = parts[1] || '';
                if (seriesName) {
                    // Формируем HTML с ссылкой на серию
                    let seriesHTML = '<a href="/s/' + encodeURIComponent(seriesName) + '">' + seriesName + '</a>';
                    if (seriesNumber) {
                        seriesHTML += ' #' + seriesNumber;
                    }
                    display.innerHTML = seriesHTML;
                    display.style.display = 'inline';
                } else {
                    display.textContent = 'Не указана';
                    display.style.display = 'inline';
                }
            } else if (fieldName === 'annotation') {
                const p = display.querySelector('p');
                if (p) {
                    p.textContent = currentValue || '';
                }
                display.style.display = 'block';
            } else if (fieldName === 'tags') {
                // Для тегов ничего не делаем при отмене, так как они обновляются по-другому
            } else {
                // Для остальных полей
                if (currentValue) {
                    display.textContent = currentValue;
                    display.style.display = 'inline';
                } else {
                    display.textContent = 'Не указан';
                    display.style.display = 'inline';
                }
            }
        }
        
        if (placeholder) {
            const currentValue = field.dataset.value;
            const hasValue = currentValue && currentValue !== '';
            placeholder.style.display = !hasValue && isEditMode ? 'inline' : 'none';
        }
        
        if (editBtn) editBtn.style.display = isEditMode ? 'inline-flex' : 'none';
        if (form) form.style.display = 'none';
    }

    // Добавить новый тег
    function addNewTag(tag, bookID) {
        if (!tag) {
            cancelAddTag();
            return;
        }
        // Ограничиваем длину тега
        if (tag.length > 16) {
            alert('Тег не может быть длиннее 16 символов');
            return;
        }
        // Отправляем запрос на сервер
        const formData = new FormData();
        formData.append('field', 'tags');
        formData.append('value', tag);
        formData.append('action', 'add');
        
        fetch(`/save/book/${encodeURIComponent(bookID)}`, {
            method: 'POST',
            body: formData
        })
        .then(response => {
            if (response.ok) {
                // Обновляем отображение тегов
                updateTagsDisplay(tag, 'add');
                cancelAddTag();
            } else {
                return response.text().then(text => {
                    throw new Error(text || `HTTP error! status: ${response.status}`);
                });
            }
        })
        .catch(error => {
            console.error('Ошибка добавления тега:', error);
            alert('Ошибка добавления тега: ' + (error.message || 'Неизвестная ошибка'));
        });
    }

    // Удалить тег
    function removeTag(tag, bookID) {
        if (!confirm('Удалить тег "' + tag + '"?')) {
            return;
        }
        // Отправляем запрос на сервер
        const formData = new FormData();
        formData.append('field', 'tags');
        formData.append('value', tag);
        formData.append('action', 'remove');
        
        fetch(`/save/book/${encodeURIComponent(bookID)}`, {
            method: 'POST',
            body: formData
        })
        .then(response => {
            if (response.ok) {
                // Обновляем отображение тегов
                updateTagsDisplay(tag, 'remove');
            } else {
                return response.text().then(text => {
                    throw new Error(text || `HTTP error! status: ${response.status}`);
                });
            }
        })
        .catch(error => {
            console.error('Ошибка удаления тега:', error);
            alert('Ошибка удаления тега: ' + (error.message || 'Неизвестная ошибка'));
        });
    }

    // Отменить добавление тега
    function cancelAddTag() {
        const addTagBtn = document.getElementById('add-tag-btn');
        const addTagForm = document.getElementById('add-tag-form');
        const newTagInput = document.getElementById('new-tag-input');
        
        if (addTagBtn) addTagBtn.style.display = 'inline-flex';
        if (addTagForm) addTagForm.style.display = 'none';
        if (newTagInput) newTagInput.value = '';
    }

    // Обновить отображение тегов
    function updateTagsDisplay(tag, action) {
        const tagsContainer = document.querySelector('.tags-list');
        if (!tagsContainer) return;
        
        const noTagsPlaceholder = tagsContainer.querySelector('.no-tags-placeholder');
        
        if (action === 'add') {
            // Убираем placeholder если он есть
            if (noTagsPlaceholder) {
                noTagsPlaceholder.remove();
            }
            // Добавляем новый тег
            const tagElement = document.createElement('span');
            tagElement.className = 'tag';
            tagElement.innerHTML = '<a href="/tag/' + encodeURIComponent(tag) + '" class="tag-link">' + tag + '</a> <button type="button" class="tag-remove-btn" data-tag="' + tag + '" title="Удалить тег"><i class="fas fa-times"></i></button>';
            
            // Вставляем перед кнопкой добавления тега, если она существует и является дочерним элементом контейнера
            const addTagBtn = document.getElementById('add-tag-btn');
            if (addTagBtn && tagsContainer.contains(addTagBtn)) {
                tagsContainer.insertBefore(tagElement, addTagBtn);
            } else {
                // Если кнопки нет или она не в контейнере, добавляем в конец
                tagsContainer.appendChild(tagElement);
            }
            
            // Добавляем обработчик удаления для нового тега
            const removeBtn = tagElement.querySelector('.tag-remove-btn');
            if (removeBtn) {
                // Предотвращаем множественное добавление обработчиков
                const clone = removeBtn.cloneNode(true);
                removeBtn.parentNode.replaceChild(clone, removeBtn);
                // Получаем bookID из data-атрибута body или другого элемента
                const body = document.querySelector('body[data-book-id]');
                const bookID = body ? body.dataset.bookId : null;
                if (bookID) {
                    clone.addEventListener('click', function() {
                        const tagToRemove = this.getAttribute('data-tag');
                        removeTag(tagToRemove, bookID);
                    });
                } else {
                    console.error('Book ID not found for tag removal');
                }
            }
        } else if (action === 'remove') {
            // Удаляем тег из DOM
            const tagElements = tagsContainer.querySelectorAll('.tag');
            tagElements.forEach(element => {
                const tagBtn = element.querySelector('.tag-remove-btn');
                if (tagBtn && tagBtn.getAttribute('data-tag') === tag) {
                    element.remove();
                }
            });
            // Показываем placeholder если тегов не осталось
            const remainingTags = tagsContainer.querySelectorAll('.tag');
            if (remainingTags.length === 0) {
                if (!noTagsPlaceholder) {
                    const placeholder = document.createElement('div');
                    placeholder.className = 'no-tags-placeholder';
                    placeholder.textContent = 'Теги не указаны';
                    const addTagBtn = document.getElementById('add-tag-btn');
                    if (addTagBtn && tagsContainer.contains(addTagBtn)) {
                        tagsContainer.insertBefore(placeholder, addTagBtn);
                    } else {
                        tagsContainer.appendChild(placeholder);
                    }
                }
            }
        }
    }

    // Установка обработчиков удаления тегов
    function setupTagRemoval(bookID) {
        document.querySelectorAll('.tag-remove-btn').forEach(btn => {
            // Проверяем, не добавлен ли уже обработчик
            const clone = btn.cloneNode(true);
            btn.parentNode.replaceChild(clone, btn);
            clone.addEventListener('click', function() {
                const tag = this.getAttribute('data-tag');
                removeTag(tag, bookID);
            });
        });
    }

    // Назначаем обработчики после загрузки страницы
    document.addEventListener('DOMContentLoaded', function() {
        //console.log("Инициализация скриптов страницы деталей книги");
        
        // Получаем bookID из data-атрибута body
        const body = document.querySelector('body[data-book-id]');
        const bookID = body ? body.dataset.bookId : null;
        if (!bookID) {
            console.error('Book ID not found in body data attribute');
            return;
        }
        
        // Обработчик клика по идентикону в шапке ---
        document.querySelectorAll('.identicon-copy-btn').forEach(img => {
            img.addEventListener('click', function() {
                const fileHash = this.getAttribute('data-filehash');
                if (!fileHash) {
                    console.warn("FileHash is empty, nothing to copy.");
                    return;
                }

                copyToClipboard(fileHash)
                    .then(() => {
                        // Визуальная обратная связь
                        // Можно использовать showCopySuccess, если она есть, или показать всплывающее сообщение
                        // Проверим, есть ли уже функция showSuccessMessage или похожая
                        // Если нет, создадим временное сообщение
                        const originalTitle = this.title;
                        this.title = 'Скопировано!';
                        const originalBorder = this.style.border;
                        this.style.border = '2px solid #28a745'; // Bootstrap success color

                        setTimeout(() => {
                            this.title = originalTitle;
                            this.style.border = originalBorder;
                        }, 2000);

                        //console.log(`FileHash ${fileHash} copied to clipboard.`);
                    })
                    .catch(err => {
                        console.error('Failed to copy FileHash: ', err);
                        alert(`Не удалось скопировать хеш: ${fileHash}`);
                    });
            });
        });

        // Обработчик увеличения обложки
        const cover = document.querySelector('.book-cover');
        if (cover) {
            cover.addEventListener('mouseenter', function(e) {
                // Проверяем, не мобильное ли устройство
                if (window.innerWidth <= 768) return;
                // Получаем позицию элемента относительно viewport
                const rect = this.getBoundingClientRect();
                // Сохраняем оригинальные координаты как CSS переменные
                this.style.setProperty('--original-left', rect.left + 'px');
                this.style.setProperty('--original-top', rect.top + 'px');
                // Добавляем класс для увеличения
                this.classList.add('enlarged');
            });
            cover.addEventListener('mouseleave', function(e) {
                this.classList.remove('enlarged');
            });
        }
        
        const editBookBtn = document.getElementById('edit-book-btn');
        const editableFields = document.querySelectorAll('.editable-field');
        const titleEditBtn = document.querySelector('.header .edit-field-btn');
        const titleDeleteBtn = document.getElementById('delete-book-btn');
        const titleEditForm = document.querySelector('.edit-field-form');
        const titleInput = titleEditForm ? titleEditForm.querySelector('.edit-field-input') : null;
        const titleSaveBtn = titleEditForm ? titleEditForm.querySelector('.save-field-btn') : null;
        const titleCancelBtn = titleEditForm ? titleEditForm.querySelector('.cancel-field-btn') : null;
        
        // Обработчик для кнопки редактирования обложки
        const editCoverBtn = document.getElementById('edit-cover-btn');
        const coverUploadInput = document.getElementById('cover-upload-input');
        const coverContainer = document.querySelector('.book-cover-container');
        
        if (editCoverBtn && coverUploadInput) {
            // Нажатие на кнопку редактирования обложки -> клик по скрытому input
            editCoverBtn.addEventListener('click', function(e) {
                e.stopPropagation(); // Предотвращаем всплытие, чтобы не закрылся режим редактирования
                coverUploadInput.click();
            });
            // Изменение значения скрытого input -> запуск загрузки
            coverUploadInput.addEventListener('change', function() {
                const file = this.files[0];
                if (file) {
                    // Получаем fileHash из data-атрибута body или другого элемента
                    const body = document.querySelector('body[data-file-hash]');
                    const fileHash = body ? body.dataset.fileHash : null;
                    if (fileHash) {
                        uploadCover(file, fileHash, bookID);
                    } else {
                        console.error('File hash not found for cover upload');
                        alert('Ошибка: не удалось получить хеш файла для загрузки обложки.');
                    }
                }
            });
        }
        
        // Проверка существования editBookBtn
        if (!editBookBtn) {
            console.error('Кнопка редактирования не найдена');
            return;
        }
        
        // Показать/скрыть режим редактирования
        editBookBtn.addEventListener('click', function() {
            try {
                const isEditMode = this.querySelector('i').classList.contains('fa-edit');
                // Переключаем видимость кнопок редактирования полей
                const fieldBtns = document.querySelectorAll('.edit-field-btn:not(.header .edit-field-btn)');
                const emptyPlaceholders = document.querySelectorAll('.empty-placeholder');
                fieldBtns.forEach(btn => {
                    btn.style.display = isEditMode ? 'inline-flex' : 'none';
                });
                // Показываем/скрываем кнопки редактирования и удаления в заголовке
                if (titleEditBtn) {
                    titleEditBtn.style.display = isEditMode ? 'inline-flex' : 'none';
                }
                if (titleDeleteBtn) {
                    titleDeleteBtn.style.display = isEditMode ? 'inline-flex' : 'none';
                }
                // Для пустых полей показываем placeholder в режиме редактирования
                emptyPlaceholders.forEach(placeholder => {
                    const field = placeholder.closest('.editable-field');
                    const display = field.querySelector('.editable-display');
                    const hasValue = display && display.textContent && display.textContent.trim() !== '';
                    placeholder.style.display = !hasValue && isEditMode ? 'inline' : 'none';
                });
                // Показываем/скрываем метку "Ограничение:" в режиме редактирования для поля без ограничений
                const over18Field = document.querySelector('.editable-field[data-field="over18"]');
                if (over18Field) {
                    const label = over18Field.querySelector('.over18-label');
                    const currentValue = over18Field.dataset.value;
                    const isChecked = currentValue === 'true';
                    if (label && !isChecked) {
                        label.style.display = isEditMode ? 'inline' : 'none';
                    }
                }
                // Меняем текст кнопки
                const icon = this.querySelector('i');
                if (icon.classList.contains('fa-edit')) {
                    icon.classList.remove('fa-edit');
                    icon.classList.add('fa-times');
                    this.title = 'Закрыть редактирование';
                    this.classList.add('edit-mode');
                    // Показываем кнопку редактирования обложки
                    if (editCoverBtn) {
                        editCoverBtn.style.display = 'flex';
                        if(coverContainer) coverContainer.classList.add('edit-mode'); // Добавляем класс
                    }
                } else {
                    icon.classList.remove('fa-times');
                    icon.classList.add('fa-edit');
                    this.title = 'Редактировать книгу';
                    this.classList.remove('edit-mode');
                    // Скрываем все открытые формы редактирования
                    document.querySelectorAll('.edit-field-form').forEach(form => {
                        form.style.display = 'none';
                    });
                    document.querySelectorAll('.edit-field-btn').forEach(btn => {
                        btn.style.display = 'none';
                    });
                    // Скрываем кнопку редактирования обложки
                    if (editCoverBtn) {
                        editCoverBtn.style.display = 'none';
                        if(coverContainer) coverContainer.classList.remove('edit-mode'); // Убираем класс
                    }
                }
            } catch (e) {
                console.error('Ошибка при переключении режима редактирования:', e);
            }
        });
        
        // Обработчик кнопки редактирования названия в заголовке
        if (titleEditBtn) {
            titleEditBtn.addEventListener('click', function(e) {
                e.stopPropagation();
                // Скрываем заголовок и показываем форму редактирования
                const titleElement = document.querySelector('.header h1');
                if (titleElement) titleElement.style.display = 'none';
                this.style.display = 'none';
                if (titleDeleteBtn) titleDeleteBtn.style.display = 'none';
                if (titleEditForm) titleEditForm.style.display = 'flex';
                // Фокус на input
                if (titleInput) {
                    titleInput.focus();
                    titleInput.select();
                }
            });
        }
        
        // Обработчик сохранения названия
        if (titleSaveBtn) {
            titleSaveBtn.addEventListener('click', function() {
                if (titleInput) {
                    const newValue = titleInput.value.trim();
                    saveTitleChange(newValue, bookID);
                }
            });
        }
        
        // Обработчик отмены редактирования названия
        if (titleCancelBtn) {
            titleCancelBtn.addEventListener('click', function() {
                cancelTitleEdit();
            });
        }
        
        // Enter и Esc для input названия
        if (titleInput) {
            titleInput.addEventListener('keydown', function(e) {
                if (e.key === 'Enter') {
                    saveTitleChange(this.value.trim(), bookID);
                } else if (e.key === 'Escape') {
                    cancelTitleEdit();
                }
            });
        }
        
        // Обработчики для каждого поля
        editableFields.forEach(field => {
            const editBtn = field.querySelector('.edit-field-btn');
            const display = field.querySelector('.editable-display');
            const placeholder = field.querySelector('.empty-placeholder');
            const form = field.querySelector('.edit-field-form');
            const saveBtn = field.querySelector('.save-field-btn');
            const cancelBtn = field.querySelector('.cancel-field-btn');
            const inputs = field.querySelectorAll('.edit-field-input');
            const textarea = field.querySelector('.edit-field-textarea');
            
            if (editBtn) {
                editBtn.addEventListener('click', function(e) {
                    e.stopPropagation();
                    if (display) display.style.display = 'none';
                    if (placeholder) placeholder.style.display = 'none';
                    editBtn.style.display = 'none';
                    form.style.display = 'flex';
                    // Фокус на первом input
                    if (inputs.length > 0) {
                        inputs[0].focus();
                        inputs[0].select();
                    } else if (textarea) {
                        textarea.focus();
                    }
                });
            }
            if (saveBtn) {
                saveBtn.addEventListener('click', function() {
                    saveFieldChanges(field, bookID);
                });
            }
            if (cancelBtn) {
                cancelBtn.addEventListener('click', function() {
                    cancelFieldEdit(field);
                });
            }
            // Enter и Esc для input полей
            inputs.forEach(input => {
                input.addEventListener('keydown', function(e) {
                    if (e.key === 'Enter') {
                        saveFieldChanges(field, bookID);
                    } else if (e.key === 'Escape') {
                        cancelFieldEdit(field);
                    }
                });
            });
            // Enter и Esc для textarea
            if (textarea) {
                textarea.addEventListener('keydown', function(e) {
                    if (e.key === 'Escape') {
                        cancelFieldEdit(field);
                    }
                });
            }
        });
        
        // Работа с тегами
        const addTagBtn = document.getElementById('add-tag-btn');
        const addTagForm = document.getElementById('add-tag-form');
        const newTagInput = document.getElementById('new-tag-input');
        const saveTagBtn = document.getElementById('save-tag-btn');
        const cancelTagBtn = document.getElementById('cancel-tag-btn');
        
        // Проверка существования элементов тегов
        if (addTagBtn && addTagForm && newTagInput && saveTagBtn && cancelTagBtn) {
            // Показать форму добавления тега
            addTagBtn.addEventListener('click', function() {
                addTagBtn.style.display = 'none';
                addTagForm.style.display = 'flex';
                newTagInput.focus();
            });
            // Сохранить новый тег
            saveTagBtn.addEventListener('click', function() {
                if (newTagInput) {
                    addNewTag(newTagInput.value.trim(), bookID);
                }
            });
            // Отменить добавление тега
            cancelTagBtn.addEventListener('click', function() {
                cancelAddTag();
            });
            // Enter для добавления тега
            newTagInput.addEventListener('keydown', function(e) {
                if (e.key === 'Enter') {
                    addNewTag(this.value.trim(), bookID);
                } else if (e.key === 'Escape') {
                    cancelAddTag();
                }
            });
        }
        
        // Инициализируем удаление тегов при загрузке
        setupTagRemoval(bookID);
        
        // Добавим обработчик удаления книги
        if (titleDeleteBtn) {
            titleDeleteBtn.addEventListener('click', function(e) {
                e.stopPropagation();
                if (confirm('Вы уверены, что хотите удалить эту книгу?\nЭто действие удалит:\n- Запись из базы данных\n- Файл книги с диска\n- Обложку с диска\nДействие нельзя отменить!')) {
                    // Отправляем запрос на удаление
                    fetch(`/delete/book/${encodeURIComponent(bookID)}`, {
                        method: 'POST'
                    })
                    .then(response => {
                        if (response.ok) {
                            // Перенаправляем на главную страницу
                            window.location.href = '/';
                        } else {
                            return response.text().then(text => {
                                throw new Error(text || `HTTP error! status: ${response.status}`);
                            });
                        }
                    })
                    .catch(error => {
                        console.error('Ошибка удаления книги:', error);
                        alert('Ошибка удаления книги: ' + (error.message || 'Неизвестная ошибка'));
                    });
                }
            });
        }
        
// Обработчик для кнопок копирования IPFS ссылки
document.querySelectorAll('.ipfs-copy-btn').forEach(button => {
    button.addEventListener('click', function() {
        const cid = this.getAttribute('data-cid');
        const fileHash = this.getAttribute('data-filehash') || '';
        const fileType = this.getAttribute('data-filetype') || '';
        
        if (!cid) {
            console.error('CID не найден для кнопки копирования');
            alert('Ошибка: не удалось получить CID для копирования');
            return;
        }
        
        // Используем шлюз из глобальной переменной (установленной в шаблоне) или по умолчанию
        const gateway = (typeof window.IPFS_GATEWAY !== 'undefined') ? window.IPFS_GATEWAY : 'https://dweb.link';
        
        // Формируем имя файла
        let fileName = fileHash;
        if (fileType) {
            fileName += '.' + fileType.toLowerCase();
        }
        
        // Формируем полную ссылку с параметром для скачивания
        const ipfsUrl = `${gateway}/ipfs/${cid}?filename=${encodeURIComponent(fileName)}`;
        
        // Копируем в буфер обмена с помощью универсальной функции
        copyToClipboard(ipfsUrl)
            .then(() => {
                // Визуальная обратная связь
                const originalText = this.innerHTML;
                this.innerHTML = '<i class="fas fa-check"></i> Скопировано!';
                this.classList.add('copied');
                setTimeout(() => {
                    this.innerHTML = originalText;
                    this.classList.remove('copied');
                }, 2000);
            })
            .catch(err => {
                console.error('Ошибка копирования: ', err);
                // Показываем диалог с ссылкой для ручного копирования
                showCopyDialog(ipfsUrl);
            });
    });
});

        //console.log("Скрипты страницы деталей книги инициализированы");
    });

    // Функция для показа диалога с ссылкой
    function showCopyDialog(link) {
        // Создаем простой диалог
        const dialog = document.createElement('div');
        dialog.style.cssText = `
            position: fixed;
            top: 50%;
            left: 50%;
            transform: translate(-50%, -50%);
            background: white;
            padding: 20px;
            border: 1px solid #ccc;
            border-radius: 5px;
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
            z-index: 10000;
            max-width: 90%;
            color: black;
        `;
        
        dialog.innerHTML = `
            <h4>Скопируйте ссылку вручную:</h4>
            <input type="text" value="${link}" readonly style="width: 100%; padding: 5px; margin: 10px 0;">
            <div style="display: flex; gap: 10px; justify-content: flex-end;">
                <button onclick="this.parentElement.parentElement.remove()" style="padding: 5px 10px; background: #6c757d; color: white; border: none; border-radius: 4px; cursor: pointer;">Закрыть</button>
            </div>
        `;
        
        document.body.appendChild(dialog);
        
        // Выделяем текст для удобства копирования
        const input = dialog.querySelector('input');
        input.select();
        input.focus();
    }

})();