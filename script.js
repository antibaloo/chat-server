class UnifiedChatManager {
    constructor(currentUsername) {
        this.currentUsername = currentUsername;
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
        await this.loadInitialUnreadCounts();
    }

    async connectGlobalSSE() {
        if (this.eventSource) this.eventSource.close();
        this.eventSource = new EventSource('http://localhost:8080/chat/global/messages');
        this.eventSource.onopen = () => {
            console.log('✅ SSE соединение установлено');
            this.reconnectAttempts = 0;
            this.showSystemMessage('Соединение установлено', 'success');
        };
        this.eventSource.onmessage = (event) => {
            try {
                const data = JSON.parse(event.data);
                this.handleGlobalMessage(data);
            } catch(e) { console.error(e); }
        };
        this.eventSource.onerror = () => this.handleConnectionError();
    }

    handleGlobalMessage(data) {
        const { chatId, message } = data;
        const isOwn = message.username === this.currentUsername;

        // 1. Обновляем локальное хранилище сообщений
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

        // 2. Определяем, есть ли элемент чата на странице
        const chatElement = document.querySelector(`.open-chat[data-chat-id='${chatId}']`);
        const isChatPresent = !!chatElement;
        
        // 3. Если чат открыт в данный момент
        if (this.activeChat === chatId) {
            this.addMessageToActiveChat(message);
            // Отмечаем сообщение как прочитанное на сервере
            if (chatData.chatClient) {
                chatData.chatClient.markAsRead();
            }
            chatData.unreadCount = 0;
            this.updateChatBadge(chatId, 0);
        }
        // 4. Если чата нет на странице → отправляем уведомление в Битрикс24
        else if (!isChatPresent && !isOwn) {
            this.sendBitrixNotification(chatId, message);
        }
        // 5. Если чат присутствует, но не активен → увеличиваем счётчик непрочитанных
        else if (!isOwn) {
            chatData.unreadCount++;
            this.updateChatBadge(chatId, chatData.unreadCount);
            this.showSystemMessage(`💬 Новое сообщение в чате ${chatId} от ${message.username}`, 'info');
        }
    }

    async sendBitrixNotification(chatId, message) {
        console.log("Отправляем уведомление в Битрикс24");
        return; //на стенде отключено
        // Вызываем AJAX-действие, как в старом GlobalNotifications
        try {
            await BX.ajax.runAction('meko:partner.Chat.getUserAndNotify', {
                data: {
                    miscountId: chatId,
                    message: message.text,
                    messageId: message.id,
                    userName: message.username,
                }
            });
        } catch(e) {
            console.error('Ошибка отправки уведомления:', e);
        }
    }

    async joinChat(chatId, username, chatname) {
        if (this.activeChat === chatId) {
            this.showSystemMessage(`⚠️ Вы уже в чате ${chatId}`, 'info');
            return;
        }
        if (this.activeChat) await this.leaveChat(this.activeChat);

        let chatData = this.chatsData.get(chatId);
        if (!chatData) {
            chatData = { messages: [], unreadCount: 0, username, chatClient: null };
            this.chatsData.set(chatId, chatData);
        }
        chatData.username = username;
        const chatClient = new UnifiedChatClient(chatId, username);
        chatData.chatClient = chatClient;

        // Загружаем историю и отмечаем прочитанным
        const messages = await chatClient.loadMessages();
        chatData.messages = messages;
        await chatClient.markAsRead(); // сброс непрочитанных на сервере
        chatData.unreadCount = 0;
        this.updateChatBadge(chatId, 0);

        this.createChatUI(chatId, username, chatname);
        this.renderMessages(chatId);
        this.activeChat = chatId;
        this.showSystemMessage(`✅ Присоединились к чату ${chatId}`, 'success');
    }

    async leaveChat(chatId) {
        const chatData = this.chatsData.get(chatId);
        if (chatData && chatData.chatClient) chatData.chatClient.disconnect();
        const el = document.getElementById(`chat-${chatId}`);
        if (el) el.remove();
        this.activeChat = null;
        this.showSystemMessage(`👋 Чат ${chatId} закрыт`, 'info');
    }

    createChatUI(chatId, username, chatname) {
        const container = document.getElementById('chatsContainer');
        const existing = document.getElementById(`chat-${chatId}`);
        if (existing) existing.remove();
        const chatCard = document.createElement('div');
        chatCard.className = 'chat-card';
        chatCard.id = `chat-${chatId}`;
        chatCard.innerHTML_old = `
            <div class="chat-header">
                <h3>${chatname}</h3>
                <button class="close-chat" onclick="window.chatManager.leaveChat(${chatId})">✕</button>
            </div>
            <div class="messages-container" id="messages-${chatId}"></div>
            <div class="chat-input">
                <input type="text" id="message-input-${chatId}" 
                       placeholder="Наберите сообщение" 
                       onkeypress="window.chatManager.handleKeyPress(event, ${chatId})">
                <button onclick="window.chatManager.sendMessage(${chatId})">Отправить ➤</button>
            </div>
        `;
        chatCard.innerHTML = `
            <div class="chat-header">
                <h3>${chatname}</h3>
                <button class="close-chat" onclick="window.chatManager.leaveChat(${chatId})">✕</button>
            </div>
            <div class="messages-container" id="messages-${chatId}"></div>
            <div class="chat-input">
                <input type="text" id="message-input-${chatId}" 
                       placeholder="Наберите сообщение" 
                       onkeypress="window.chatManager.handleKeyPress(event, ${chatId})">
                <button class="attach-btn" onclick="window.chatManager.attachFile(${chatId})">📤</button>
                <button onclick="window.chatManager.sendMessage(${chatId})">Отправить ➤</button>
            </div>
            <input type="file" id="file-input-${chatId}" style="display:none" accept="image/*,application/pdf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document">
        `;
        container.appendChild(chatCard);
        container.style.display = "flex";
        setTimeout(() => document.getElementById(`message-input-${chatId}`)?.focus(), 150);

        // после добавления chatCard в DOM
        const fileInput = document.getElementById(`file-input-${chatId}`);
        if (fileInput) {
            fileInput.addEventListener('change', (e) => {
                if (e.target.files.length > 0) {
                    this.handleFileSelect(chatId, e.target.files[0]);
                    e.target.value = ''; // сброс для повторного выбора
                }
            });
        }
    }

    /*
    // определить, является ли файл изображением
    const isImage = /\.(jpg|jpeg|png|gif|webp)$/i.test(msg.file_url);
    const fileHtml = msg.file_url ? (
        isImage 
            ? `<div class="message-attachment"><img src="${this.escapeHtml(msg.file_url)}" alt="image" style="max-width: 200px; max-height: 200px;"></div>`
            : `<div class="message-attachment"><a href="${this.escapeHtml(msg.file_url)}" target="_blank">📤 ${this.escapeHtml(msg.file_name || 'Файл')}</a></div>`
    ) : '';
    */

    renderMessages(chatId) {
        const chatData = this.chatsData.get(chatId);
        const container = document.getElementById(`messages-${chatId}`);
        if (!container || !chatData) return;
        if (!chatData.messages.length) {
            container.innerHTML = '<div class="empty-chat">💬 Пока нет сообщений</div>';
            return;
        }
        const html = chatData.messages.map(msg => {
            const isOwn = msg.user_name === chatData.username;
            const fileHtml = msg.file_url ? `
                <div class="message-attachment">
                    <a href="${this.escapeHtml(msg.file_url)}" target="_blank" download="${this.escapeHtml(msg.file_name || 'file')}">
                        📤 ${this.escapeHtml(msg.file_name || 'Файл')}
                    </a>
                </div>
            ` : '';
            return `
                <div class="message ${isOwn ? 'message-own' : 'message-other'}">
                    <div class="message-bubble">
                        <span class="message-username">${this.escapeHtml(msg.user_name)}</span>
                        <div class="message-text">${this.escapeHtml(msg.text)}</div>
                        ${fileHtml}
                        <span class="message-time">${this.formatTime(msg.created_at)}</span>
                    </div>
                </div>
            `;
        }).join('');
        container.innerHTML = html;
        container.scrollTop = container.scrollHeight;
    }

    addMessageToActiveChat(message) {
        const chatId = this.activeChat;
        const container = document.getElementById(`messages-${chatId}`);
        if (!container) return;
        const chatData = this.chatsData.get(chatId);
        const isOwn = message.user_name === chatData.username;
        const msgDiv = document.createElement('div');
        msgDiv.className = `message ${isOwn ? 'message-own' : 'message-other'}`;

        const fileHtml = message.file_url ? `
            <div class="message-attachment">
                <a href="${this.escapeHtml(message.file_url)}" target="_blank" download="${this.escapeHtml(message.file_name || 'file')}">
                    📤 ${this.escapeHtml(message.file_name || 'Файл')}
                </a>
            </div>
        ` : '';

        msgDiv.innerHTML = `
            <div class="message-bubble">
                <span class="message-username">${this.escapeHtml(message.user_name)}</span>
                <div class="message-text">${this.escapeHtml(message.text)}</div>
                ${fileHtml}
                <span class="message-time">${this.formatTime(message.created_at)}</span>
            </div>
        `;
        container.appendChild(msgDiv);
        container.scrollTop = container.scrollHeight;
    }

    async sendMessage(chatId) {
        const chatData = this.chatsData.get(chatId);
        if (!chatData || !chatData.chatClient) return;
        const input = document.getElementById(`message-input-${chatId}`);
        const text = input?.value.trim();
        const fileData = chatData.pendingFile || null; // { file_url, file_name }
        if (!text && !fileData) return;
        const ok = await chatData.chatClient.sendMessage(text, fileData);
        if (ok) input.value = '';
        else this.showSystemMessage('❌ Не удалось отправить', 'error');
    }


    async loadInitialUnreadCounts() {
        const buttons = document.querySelectorAll('.open-chat');
        for (const btn of buttons) {
            const chatId = parseInt(btn.dataset.chatId);
            const username = this.currentUsername;
            if (!chatId || !username) continue;
            const client = new UnifiedChatClient(chatId, username);
            const count = await client.getUnreadCount();
            this.updateChatBadge(chatId, count);
            // сохраняем данные чата
            if (!this.chatsData.has(chatId)) {
                this.chatsData.set(chatId, {
                    messages: [],
                    unreadCount: count,
                    username: username,
                    chatClient: null
                });
            } else {
                this.chatsData.get(chatId).unreadCount = count;
            }
        }
    }

    updateChatBadge(chatId, count) {
        const buttons = document.querySelectorAll(`.open-chat[data-chat-id='${chatId}']`);
        buttons.forEach(btn => {
            const oldBadge = btn.querySelector('.unread-badge');
            if (oldBadge) oldBadge.remove();
            if (count > 0) {
                const badge = document.createElement('span');
                badge.className = 'unread-badge';
                badge.textContent = count > 99 ? '99+' : count;
                btn.style.position = 'relative';
                btn.appendChild(badge);
            } else {
                btn.style.position = '';
            }
        });
    }

    handleConnectionError() {
        if (this.reconnectAttempts < this.maxReconnectAttempts) {
            this.reconnectAttempts++;
            const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1);
            this.showSystemMessage(`⚠️ Потеря соединения, попытка ${this.reconnectAttempts}...`, 'error');
            setTimeout(() => this.connectGlobalSSE(), delay);
        } else {
            this.showSystemMessage('❌ Соединение потеряно. Перезагрузите страницу.', 'error');
        }
    }

    showSystemMessage(text, type) {
        const statusDiv = document.getElementById('status');
        if (!statusDiv) return;
        statusDiv.textContent = text;
        statusDiv.style.opacity = '1';
        const colors = { error: '#f44336', success: '#4caf50', info: '#2196f3' };
        statusDiv.style.backgroundColor = colors[type] || '#666';
        statusDiv.style.padding = '10px 20px';
        setTimeout(() => { statusDiv.style.opacity = '0'; setTimeout(() => { if (statusDiv.textContent === text) statusDiv.textContent = ''; }, 500); }, 3000);
    }
    
    formatTime(created_at) {
        const date = new Date(created_at);
        return date.toLocaleTimeString([], { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
    }
    
    handleKeyPress(event, chatId) {
        if (event.key === 'Enter') {
            this.sendMessage(chatId);
        }
    }

    escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    handleKeyPress(e, chatId) { if (e.key === 'Enter') this.sendMessage(chatId); }

    setupEventListeners() {
        if ('Notification' in window && Notification.permission === 'default') Notification.requestPermission();
        window.addEventListener('beforeunload', () => this.eventSource?.close());
    }

    attachFile(chatId) {
        const input = document.getElementById(`file-input-${chatId}`);
        if (input) input.click();
    }

    async handleFileSelect(chatId, file) {
        const chatData = this.chatsData.get(chatId);
        if (!chatData || !chatData.chatClient) return;
        try {
            const result = await chatData.chatClient.uploadFile(file);
            chatData.pendingFile = { file_url: result.file_url, file_name: result.file_name };
            // После загрузки можно либо сразу отправить сообщение с ссылкой,
            // либо вставить ссылку в поле ввода.
            const input = document.getElementById(`message-input-${chatId}`);
            if (input) {
                const linkText = `${result.file_name} (${this.formatFileSize(result.size)})`;
                input.value = `📤 ${linkText}: ${result.file_url}`;
            }
            // Можно также сразу отправить, но лучше дать пользователю возможность добавить текст.
        } catch(e) {
            this.showSystemMessage('❌ Ошибка загрузки файла', 'error');
        }
    }

    formatFileSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1048576) return (bytes/1024).toFixed(1) + ' KB';
        return (bytes/1048576).toFixed(1) + ' MB';
    }
}

// Класс UnifiedChatClient клиент для работы с API
class UnifiedChatClient {
    constructor(chatId, username) {
        this.chatId = chatId;
        this.username = username;
    }

    async uploadFile(file) {
        const formData = new FormData();
        formData.append('file', file);
        const resp = await fetch(`http://localhost:8080/chat/${this.chatId}/upload`, {
            method: 'POST',
            body: formData
        });
        if (!resp.ok) {
            throw new Error('Upload failed');
        }
        return await resp.json(); // { file_url, file_name, size }
    }

    async loadMessages() {
        const resp = await fetch(`http://localhost:8080/chat/${this.chatId}`);
        return resp.ok ? await resp.json() : [];
    }
    
    async sendMessage(text, fileData = null) {
        const payload = { user_name: this.username, text };
        if (fileData) {
            payload.file_url = fileData.file_url;
            payload.file_name = fileData.file_name;
        }
        const resp = await fetch(`http://localhost:8080/chat/${this.chatId}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        return resp.ok;
    }

    async markAsRead() {
        await fetch(`http://localhost:8080/chat/${this.chatId}/read`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ user_name: this.username })
        });
    }
    async getUnreadCount() {
        const resp = await fetch(`http://localhost:8080/chat/${this.chatId}/unread?user_name=${encodeURIComponent(this.username)}`);
        if (!resp.ok) return 0;
        const data = await resp.json();
        return data.unread || 0;
    }

    disconnect() {}
}


// Инициализация при загрузке страницы
document.addEventListener("DOMContentLoaded", async () => { // BX.ready( <=> document.addEventListener("DOMContentLoaded",
    // Получите имя текущего пользователя из Битрикс24
    let userName = 'Пользователь';
    const bitrixUserNameContainer = document.getElementById("bitrixUserName");
    if (bitrixUserNameContainer) userName = bitrixUserNameContainer.innerHTML; 
    const currentUsername = userName;
    window.chatManager = new UnifiedChatManager(currentUsername);
    await window.chatManager.initialize();

    // Навесить обработчики на кнопки .open-chat
    document.querySelectorAll('.open-chat').forEach(btn => {
        btn.removeEventListener('click', window.joinChat);
        btn.addEventListener('click', window.joinChat);
    });
});

window.joinChat = function(event) {
    const chatId = Number(event.currentTarget.dataset.chatId);
    const chatName = event.currentTarget.dataset.chatName;
    // Получите имя текущего пользователя из Битрикс24
    let userName = 'Пользователь';
    const bitrixUserNameContainer = document.getElementById("bitrixUserName");
    if (bitrixUserNameContainer) userName =bitrixUserNameContainer.innerHTML; 
    window.chatManager.joinChat(chatId, userName, chatName);
};