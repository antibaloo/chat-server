class GlobalChatManager {
    constructor() {
        this.eventSource = null;
        this.activeChat = null; // Текущий открытый чат
        this.chatsData = new Map(); // Хранилище данных чатов: chatId -> { messages, unreadCount, username, chatClient }
        this.isConnecting = false;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 5;
        this.reconnectDelay = 3000;
    }

    async initialize() {
        await this.connectGlobalSSE();
        this.setupEventListeners();
    }

    async connectGlobalSSE() {
        if (this.eventSource) {
            this.eventSource.close();
        }

        this.isConnecting = true;
        
        try {
            this.eventSource = new EventSource('http://localhost:8080/chat/global/messages/');
            
            this.eventSource.onopen = () => {
                console.log('✅ Глобальное SSE соединение установлено');
                this.isConnecting = false;
                this.reconnectAttempts = 0;
                this.showSystemMessage('Соединение с сервером установлено', 'success');
            };
            
            this.eventSource.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    this.handleGlobalMessage(data);
                } catch (error) {
                    console.error('Ошибка парсинга сообщения:', error);
                }
            };
            
            this.eventSource.onerror = (error) => {
                console.error('Ошибка SSE соединения:', error);
                this.handleConnectionError();
            };
            
        } catch (error) {
            console.error('Ошибка создания SSE соединения:', error);
            this.handleConnectionError();
        }
    }

    handleGlobalMessage(data) {
        const { chatId, message } = data;
        
        // Обновляем данные чата
        if (!this.chatsData.has(chatId)) {
            this.chatsData.set(chatId, {
                messages: [],
                unreadCount: 0,
                username: null,
                chatClient: null
            });
        }
        
        const chatData = this.chatsData.get(chatId);
        chatData.messages.push(message);
        
        // Если это активный чат - показываем сообщение сразу
        if (this.activeChat === chatId) {
            this.addMessageToActiveChat(message);
            chatData.unreadCount = 0;
            this.updateChatBadge(chatId, 0);
        } else {
            // Иначе увеличиваем счетчик непрочитанных
            chatData.unreadCount++;
            this.updateChatBadge(chatId, chatData.unreadCount);
            
            // Показываем уведомление
            this.showNotification(message);
        }
    }

    async joinChat(chatId, username) {
        // Проверяем, не открыт ли уже этот чат
        if (this.activeChat === chatId) {
            this.showSystemMessage(`⚠️ Вы уже в чате ${chatId}`, 'info');
            return;
        }
        
        // Закрываем текущий чат если есть
        if (this.activeChat) {
            await this.leaveChat(this.activeChat);
        }
        
        this.showSystemMessage(`🔌 Присоединяемся к чату ${chatId}...`, 'info');
        
        // Получаем или создаем данные чата
        let chatData = this.chatsData.get(chatId);
        if (!chatData) {
            chatData = {
                messages: [],
                unreadCount: 0,
                username: username,
                chatClient: null
            };
            this.chatsData.set(chatId, chatData);
        } else {
            chatData.username = username;
        }
        
        // Создаем клиент чата для отправки сообщений
        const chatClient = new ChatClient(chatId, username);
        chatData.chatClient = chatClient;
        
        // Загружаем историю сообщений
        const messages = await chatClient.loadMessages();
        chatData.messages = messages;
        
        // Создаем UI чата
        this.createChatUI(chatId, username);
        
        // Рендерим сообщения
        setTimeout(() => {
            this.renderMessages(chatId);
        }, 100);
        
        this.activeChat = chatId;
        chatData.unreadCount = 0;
        this.updateChatBadge(chatId, 0);
        
        this.showSystemMessage(`✅ Успешно присоединились к чату ${chatId}!`, 'success');
    }

    async leaveChat(chatId) {
        const chatData = this.chatsData.get(chatId);
        if (chatData && chatData.chatClient) {
            // Не отключаем клиент, просто закрываем UI
            chatData.chatClient.disconnect();
        }
        
        // Удаляем UI чата
        const chatElement = document.getElementById(`chat-${chatId}`);
        if (chatElement) {
            chatElement.style.animation = 'slideOut 0.3s ease-in forwards';
            setTimeout(() => {
                chatElement.remove();
                
                // Если больше нет открытых чатов, скрываем контейнер
                const container = document.getElementById('chatsContainer');
                if (container.children.length === 0) {
                    container.style.display = 'none';
                }
            }, 300);
        }
        
        this.activeChat = null;
        this.showSystemMessage(`👋 Чат ${chatId} закрыт`, 'info');
    }

    createChatUI(chatId, username) {
        const container = document.getElementById('chatsContainer');
        
        // Удаляем старый UI если есть
        const oldChat = document.getElementById(`chat-${chatId}`);
        if (oldChat) {
            oldChat.remove();
        }
        
        const chatCard = document.createElement('div');
        chatCard.className = 'chat-card';
        chatCard.id = `chat-${chatId}`;
        
        chatCard.innerHTML = `
            <div class="chat-header">
                <h3>💬 Чат по товарной позиции ${chatId}</h3>
                <button class="close-chat" onclick="window.chatManager.leaveChat(${chatId})">✕</button>
            </div>
            <div class="messages-container" id="messages-${chatId}">
                <div class="empty-chat">⏳ Загрузка сообщений...</div>
            </div>
            <div class="chat-input">
                <input type="text" id="message-input-${chatId}" 
                        placeholder="Наберите сообщение" 
                        onkeypress="window.chatManager.handleKeyPress(event, ${chatId})"
                        autocomplete="off">
                <button onclick="window.chatManager.sendMessage(${chatId})">Отправить ➤</button>
            </div>
        `;
        
        container.appendChild(chatCard);
        container.style.display = "flex";
        
        // Фокусируемся на поле ввода
        setTimeout(() => {
            const input = document.getElementById(`message-input-${chatId}`);
            if (input) input.focus();
        }, 150);
    }

    renderMessages(chatId) {
        const chatData = this.chatsData.get(chatId);
        if (!chatData) return;
        
        const container = document.getElementById(`messages-${chatId}`);
        if (!container) return;
        
        if (!chatData.messages || chatData.messages.length === 0) {
            container.innerHTML = '<div class="empty-chat">💬 Пока нет сообщений. Отправьте первое сообщение!</div>';
            return;
        }
        
        const messagesHtml = chatData.messages.map(msg => {
            const isOwn = msg.username === chatData.username;
            return `
                <div class="message ${isOwn ? 'message-own' : 'message-other'}">
                    <div class="message-bubble">
                        <span class="message-username">${this.escapeHtml(msg.username)}</span>
                        <div class="message-text">${this.escapeHtml(msg.text)}</div>
                        <span class="message-time">${this.formatTime(msg.timestamp)}</span>
                    </div>
                </div>
            `;
        }).join('');
        
        container.innerHTML = messagesHtml;
        container.scrollTop = container.scrollHeight;
    }

    addMessageToActiveChat(message) {
        if (!this.activeChat) return;
        
        const container = document.getElementById(`messages-${this.activeChat}`);
        if (!container) return;
        
        const chatData = this.chatsData.get(this.activeChat);
        if (!chatData) return;
        
        // Удаляем пустое состояние если есть
        if (container.querySelector('.empty-chat')) {
            container.innerHTML = '';
        }
        
        const isOwn = message.username === chatData.username;
        const messageElement = document.createElement('div');
        messageElement.className = `message ${isOwn ? 'message-own' : 'message-other'}`;
        messageElement.innerHTML = `
            <div class="message-bubble">
                <span class="message-username">${this.escapeHtml(message.username)}</span>
                <div class="message-text">${this.escapeHtml(message.text)}</div>
                <span class="message-time">${this.formatTime(message.timestamp)}</span>
            </div>
        `;
        
        container.appendChild(messageElement);
        container.scrollTop = container.scrollHeight;
    }

    async sendMessage(chatId) {
        const chatData = this.chatsData.get(chatId);
        if (!chatData || !chatData.chatClient) return;
        
        const input = document.getElementById(`message-input-${chatId}`);
        if (!input) return;
        
        const text = input.value;
        if (!text.trim()) return;
        
        const success = await chatData.chatClient.sendMessage(text);
        if (success) {
            input.value = '';
        } else {
            this.showSystemMessage('❌ Не удалось отправить сообщение', 'error');
        }
    }

    updateChatBadge(chatId, count) {
        // Находим все кнопки чата с этим ID
        const chatButtons = document.querySelectorAll(`.open-chat[data-chat-id='${chatId}']`);
        
        chatButtons.forEach(button => {
            // Удаляем старый бейдж
            const oldBadge = button.querySelector('.unread-badge');
            if (oldBadge) {
                oldBadge.remove();
            }
            
            // Добавляем новый если есть непрочитанные
            if (count > 0) {
                const badge = document.createElement('span');
                badge.className = 'unread-badge';
                badge.textContent = count > 99 ? '99+' : count;
                button.style.position = 'relative';
                button.appendChild(badge);
            } else {
                // Если счетчик 0, просто удаляем бейдж
                button.style.position = '';
            }
        });
    }

    showNotification(message) {
        // Показываем системное уведомление если разрешено
        if (Notification.permission === 'granted') {
            new Notification(`Новое сообщение от ${message.username}`, {
                body: message.text,
                icon: '/favicon.ico'
            });
        }
        
        // Показываем в статусной строке
        this.showSystemMessage(`💬 Новое сообщение от ${message.username}: ${message.text.substring(0, 50)}`, 'info');
    }

    showSystemMessage(message, type) {
        const statusDiv = document.getElementById('status');
        statusDiv.textContent = message;
        statusDiv.style.opacity = '1';
        
        const colors = {
            error: '#f44336',
            success: '#4caf50',
            info: '#2196f3'
        };
        
        statusDiv.style.backgroundColor = colors[type] || '#666';
        statusDiv.style.padding = '10px 20px';
        statusDiv.style.borderRadius = '25px';
        statusDiv.style.boxShadow = '0 2px 10px rgba(0,0,0,0.2)';
        
        setTimeout(() => {
            statusDiv.style.opacity = '0';
            setTimeout(() => {
                if (statusDiv.textContent === message) {
                    statusDiv.textContent = '';
                    statusDiv.style.padding = '0';
                }
            }, 500);
        }, 3000);
    }

    handleConnectionError() {
        if (this.reconnectAttempts < this.maxReconnectAttempts) {
            this.reconnectAttempts++;
            const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1);
            this.showSystemMessage(`⚠️ Потеря соединения. Попытка переподключения ${this.reconnectAttempts}/${this.maxReconnectAttempts}...`, 'error');
            
            setTimeout(() => {
                this.connectGlobalSSE();
            }, delay);
        } else {
            this.showSystemMessage('❌ Не удалось восстановить соединение. Перезагрузите страницу.', 'error');
        }
    }

    handleKeyPress(event, chatId) {
        if (event.key === 'Enter') {
            this.sendMessage(chatId);
        }
    }

    formatTime(timestamp) {
        const date = new Date(timestamp);
        return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    setupEventListeners() {
        // Запрашиваем разрешение на уведомления
        if ('Notification' in window && Notification.permission === 'default') {
            Notification.requestPermission();
        }
        
        // Обработка закрытия страницы
        window.addEventListener('beforeunload', () => {
            if (this.eventSource) {
                this.eventSource.close();
            }
        });
    }
}

// Класс ChatClient для отправки сообщений (упрощенный)
class ChatClient {
    constructor(chatId, username) {
        this.chatId = chatId;
        this.username = username;
    }

    async loadMessages() {
        try {
            const response = await fetch(`http://localhost:8080/chat/${this.chatId}/`);
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            const messages = await response.json();
            return messages || [];
        } catch (error) {
            console.error('Ошибка загрузки сообщений:', error);
            return [];
        }
    }

    async sendMessage(text) {
        if (!text.trim()) return false;
        
        try {
            const response = await fetch(`http://localhost:8080/chat/${this.chatId}/`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    username: this.username,
                    text: text.trim()
                })
            });
            
            if (response.ok) {
                return true;
            }
        } catch (error) {
            console.error('Ошибка отправки сообщения:', error);
        }
        return false;
    }

    disconnect() {
        // В новой версии не нужно отключать SSE соединение
    }
}

// Функция для обработки клика по чату
async function joinChat(event) {
    const chatId = Number(event.currentTarget.dataset.chatId);
    const username = event.currentTarget.dataset.userName;
    
    if (window.chatManager) {
        await window.chatManager.joinChat(chatId, username);
    }
}

// Инициализация при загрузке страницы
document.addEventListener("DOMContentLoaded", async () => {
    window.chatManager = new GlobalChatManager();
    await window.chatManager.initialize();
    
    // Добавляем обработчики для всех кнопок чата
    const chatButtons = document.querySelectorAll('.open-chat');
    chatButtons.forEach(button => {
        // Удаляем старый обработчик если есть
        button.removeEventListener('click', joinChat);
        button.addEventListener('click', joinChat);
    });
});