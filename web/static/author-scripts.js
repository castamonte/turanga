// web/static/author-scripts.js

(function() {
    'use strict';

    document.addEventListener('DOMContentLoaded', function() {
        //console.log("Инициализация скриптов страницы автора");

        const editAuthorBtn = document.getElementById('edit-author-btn');
        const titleEditForm = document.querySelector('.edit-field-form');
        const titleInput = titleEditForm ? titleEditForm.querySelector('.edit-field-input') : null;
        const titleSaveBtn = titleEditForm ? titleEditForm.querySelector('.save-field-btn') : null;
        const displayName = document.getElementById('author-name-display');

        if (!editAuthorBtn) {
            console.warn('Кнопка редактирования автора не найдена на странице');
            return;
        }

        // --- НОВАЯ ПЕРЕМЕННАЯ ДЛЯ ОТСЛЕЖИВАНИЯ РЕЖИМА РЕДАКТИРОВАНИЯ ---
        let isEditing = false;

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
                    if (titleInput) {
                        titleInput.focus();
                        titleInput.select();
                    }
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

        function saveAuthorName(value) {
            const authorIdElement = document.querySelector('[data-author-id]');
            const authorId = authorIdElement ? authorIdElement.dataset.authorId : null;
            
            if (!authorId) {
                console.error('ID автора не найден');
                alert('Ошибка: ID автора не найден');
                return;
            }

            if (!value.trim()) {
                alert('Имя автора не может быть пустым');
                return;
            }

            const formData = new FormData();
            formData.append('name', value);

            fetch(`/save/author/${authorId}`, {
                method: 'POST',
                body: formData
            })
            .then(response => {
                if (response.ok) {
                    if (displayName) displayName.textContent = value;
                    const pageTitle = document.querySelector('title');
                    if (pageTitle) {
                         pageTitle.textContent = value + ' - Turanga';
                    }
                    cancelAuthorEdit(); // Закрываем режим редактирования после сохранения
                } else {
                    return response.text().then(text => {
                        throw new Error(text || `Ошибка сервера: ${response.status}`);
                    });
                }
            })
            .catch(error => {
                console.error('Ошибка сохранения имени автора:', error);
                alert('Ошибка сохранения: ' + (error.message || 'Неизвестная сетевая ошибка'));
            });
        }

        if (titleSaveBtn) {
            titleSaveBtn.addEventListener('click', function() {
                if (titleInput) {
                    saveAuthorName(titleInput.value.trim());
                }
            });
        }

        // --- ОБНОВЛЕННАЯ ФУНКЦИЯ ОТМЕНЫ ---
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
            // --- СБРАСЫВАЕМ ФЛАГ РЕДАКТИРОВАНИЯ ---
            isEditing = false;
        }

        if (titleInput) {
            titleInput.addEventListener('keydown', function(e) {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    saveAuthorName(this.value.trim());
                }
                // --- ДОБАВЛЯЕМ ОБРАБОТКУ ESC ---
                else if (e.key === 'Escape') {
                    e.preventDefault();
                    cancelAuthorEdit();
                }
            });
        }

        // --- ДОБАВЛЯЕМ ГЛОБАЛЬНУЮ ОБРАБОТКУ ESC ДЛЯ ДОКУМЕНТА ---
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