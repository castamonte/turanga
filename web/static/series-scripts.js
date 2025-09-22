// web/static/series-scripts.js

(function() {
    'use strict';

    document.addEventListener('DOMContentLoaded', function() {
        //console.log("Инициализация скриптов страницы серии");

        const editSeriesBtn = document.getElementById('edit-series-btn');
        const titleEditForm = document.querySelector('.edit-field-form');
        const titleInput = titleEditForm ? titleEditForm.querySelector('.edit-field-input') : null;
        const titleSaveBtn = titleEditForm ? titleEditForm.querySelector('.save-field-btn') : null;
        const displayName = document.getElementById('series-name-display');

        if (!editSeriesBtn) {
            console.warn('Кнопка редактирования серии не найдена на странице');
            return;
        }

        // --- НОВАЯ ПЕРЕМЕННАЯ ДЛЯ ОТСЛЕЖИВАНИЯ РЕЖИМА РЕДАКТИРОВАНИЯ ---
        let isEditing = false;

        editSeriesBtn.addEventListener('click', function() {
            try {
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
                    this.title = 'Редактировать серию';
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

        function saveSeriesName(value) {
            const seriesNameEncoded = editSeriesBtn ? editSeriesBtn.getAttribute('data-series-name') : '';
            
            if (!seriesNameEncoded) {
                console.error('Закодированное имя серии не найдено в data-атрибуте кнопки редактирования');
                alert('Ошибка: не удалось определить исходное имя серии.');
                return;
            }

            if (!value.trim()) {
                alert('Название серии не может быть пустым');
                return;
            }

            const formData = new FormData();
            formData.append('name', value);

            fetch('/save/series/' + seriesNameEncoded, {
                method: 'POST',
                body: formData
            })
            .then(response => {
                //console.log("Ответ от сервера при сохранении серии:", response.status, response.statusText);
                if (response.ok) {
                    window.location.href = '/s/' + encodeURIComponent(value);
                } else {
                    return response.text().then(text => {
                        throw new Error(text || `Ошибка сервера: ${response.status}`);
                    });
                }
            })
            .catch(error => {
                console.error('Ошибка сохранения названия серии:', error);
                alert('Ошибка сохранения: ' + (error.message || 'Неизвестная сетевая ошибка'));
            });
        }

        if (titleSaveBtn) {
            titleSaveBtn.addEventListener('click', function() {
                if (titleInput) {
                    saveSeriesName(titleInput.value.trim());
                }
            });
        }

        // --- ФУНКЦИЯ ОТМЕНЫ ДЛЯ СЕРИИ ---
        function cancelSeriesEdit() {
            if (displayName) displayName.style.display = 'block';
            if (titleEditForm) titleEditForm.style.display = 'none';
            const icon = editSeriesBtn.querySelector('i');
            if (icon && icon.classList.contains('fa-times')) {
                icon.classList.remove('fa-times');
                icon.classList.add('fa-edit');
                editSeriesBtn.title = 'Редактировать серию';
                editSeriesBtn.classList.remove('edit-mode');
            }
            // --- СБРАСЫВАЕМ ФЛАГ РЕДАКТИРОВАНИЯ ---
            isEditing = false;
        }

        if (titleInput) {
            titleInput.addEventListener('keydown', function(e) {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    saveSeriesName(this.value.trim());
                }
                // --- ДОБАВЛЯЕМ ОБРАБОТКУ ESC ---
                else if (e.key === 'Escape') {
                    e.preventDefault();
                    cancelSeriesEdit();
                }
            });
        }

        // --- ДОБАВЛЯЕМ ГЛОБАЛЬНУЮ ОБРАБОТКУ ESC ДЛЯ ДОКУМЕНТА ---
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && isEditing) {
                e.preventDefault();
                //console.log("Нажат Esc, выходим из режима редактирования серии");
                cancelSeriesEdit();
            }
        });

        //console.log("Скрипты страницы серии инициализированы");
    });

})();