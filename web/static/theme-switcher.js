// theme-switcher.js
function initThemeSwitcher() {
    const toggleButton = document.getElementById('theme-toggle');
    
    if (!toggleButton) {
        // Если кнопка переключателя нет на странице, просто применяем сохранённую тему
        applySavedTheme();
        return;
    }

    const icon = toggleButton.querySelector('i');
    
    // Проверяем сохранённую тему в localStorage или настройки системы
    const savedTheme = localStorage.getItem('theme');
    const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    
    // Устанавливаем начальную тему
    if (savedTheme) {
        document.documentElement.setAttribute('data-theme', savedTheme);
        if (icon) updateIcon(icon, savedTheme, toggleButton);
    } else if (systemPrefersDark) {
        document.documentElement.setAttribute('data-theme', 'dark');
        if (icon) updateIcon(icon, 'dark', toggleButton);
    } else {
        document.documentElement.setAttribute('data-theme', 'light');
        if (icon) updateIcon(icon, 'light', toggleButton);
    }
    
    // Функция переключения темы
    function toggleTheme() {
        const currentTheme = document.documentElement.getAttribute('data-theme');
        const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
        
        document.documentElement.setAttribute('data-theme', newTheme);
        localStorage.setItem('theme', newTheme);
        if (icon) updateIcon(icon, newTheme, toggleButton);
    }
    
    // Функция обновления иконки
    function updateIcon(icon, theme, button) {
        if (theme === 'dark') {
            icon.classList.remove('fa-moon');
            icon.classList.add('fa-sun');
            button.title = 'Светлая тема';
        } else {
            icon.classList.remove('fa-sun');
            icon.classList.add('fa-moon');
            button.title = 'Тёмная тема';
        }
    }
    
    // Назначаем обработчик события
    toggleButton.addEventListener('click', toggleTheme);
}

// Функция для применения темы без переключателя (для страниц без кнопки)
function applySavedTheme() {
    const savedTheme = localStorage.getItem('theme');
    const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    
    if (savedTheme) {
        document.documentElement.setAttribute('data-theme', savedTheme);
    } else if (systemPrefersDark) {
        document.documentElement.setAttribute('data-theme', 'dark');
    } else {
        document.documentElement.setAttribute('data-theme', 'light');
    }
}

// Инициализируем тему когда DOM загружен
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initThemeSwitcher);
} else {
    initThemeSwitcher();
}
