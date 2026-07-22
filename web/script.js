class ChatManager {
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
        this.startObservingChatButtons();
    }

    async connectGlobalSSE() {
        if (this.eventSource) this.eventSource.close();
        this.eventSource = new EventSource('/chat/global/messages');
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

    async loadInitialUnreadCounts() {
        let buttons = document.querySelectorAll('.open-chat');
        for (const btn of buttons) {
            await this.registerChatButton(btn);
        }
    }

    startObservingChatButtons() {
        const observer = new MutationObserver((mutations) => {
            for (const mutation of mutations) {
                if (mutation.type === 'childList' && mutation.addedNodes.length) {
                    for (const node of mutation.addedNodes) {
                        if (node.nodeType === Node.ELEMENT_NODE) {
                            if (node.matches && node.matches('.open-chat')) {
                                this.registerChatButton(node);
                            } else if (node.querySelectorAll) {
                                const btns = node.querySelectorAll('.open-chat');
                                btns.forEach(btn => this.registerChatButton(btn));
                            }
                        }
                    }
                }
            }
        });
        observer.observe(document.body, { childList: true, subtree: true });
    }

    async registerChatButton(btn) {
        if (btn.dataset.chatRegistered === 'true') return;
        btn.dataset.chatRegistered = 'true';

        const chatId = btn.dataset.chatId;
        const userName = btn.dataset.userName;
        if (!chatId) return;

        // Устанавливаем обработчик
        btn.removeEventListener('click', window.joinChat);
        btn.addEventListener('click', window.joinChat);

        // Загружаем и отображаем непрочитанные
        if (userName) {
            const client = new UnifiedChatClient(chatId, userName);
            const unread = await client.getUnreadCount();
            if (!this.chatsData.has(chatId)) {
                this.chatsData.set(chatId, {
                    messages: [],
                    unreadCount: unread,
                    username: userName,
                    chatClient: null,
                    pendingAttachments: []
                });
            } else {
                this.chatsData.get(chatId).unreadCount = unread;
            }
            this.updateChatBadge(chatId, unread);
        }
    }
    
    handleGlobalMessage(data) {
        const { chatId, message } = data;
        const isOwn = message.user_name === this.currentUsername;
        // 1. Обновляем локальное хранилище сообщений
        if (!this.chatsData.has(chatId)) {
            this.chatsData.set(chatId, {
                messages: [],
                unreadCount: 0,
                username: null,
                chatClient: null,
                pendingAttachments: []
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
        // 4. Если чат присутствует, но не активен → увеличиваем счётчик непрочитанных
        else if (isChatPresent && !isOwn) {
            chatData.unreadCount++;
            this.updateChatBadge(chatId, chatData.unreadCount);
            this.showSystemMessage(`💬 Новое сообщение в чате ${chatId} от ${message.user_name}`, 'info');
        }
    }

    async sendBitrixNotification(chatId, message) {
        // Вызываем AJAX-действие, как в старом GlobalNotifications
        try {
            await BX.ajax.runAction('meko:partner.Chat.getUserAndNotify', {
                data: {
                    miscountId: chatId,
                    message: message.text,
                    messageId: message.id,
                    userName: message.user_name,
                }
            });
        } catch(e) {
            console.error('Ошибка отправки уведомления:', e);
        }
    }

    async joinChat(chatId, username, chatname, withFiles) {
        if (this.activeChat === chatId) {
            this.showSystemMessage(`⚠️ Вы уже в чате ${chatId}`, 'info');
            return;
        }
        if (this.activeChat) await this.leaveChat(this.activeChat);

        let chatData = this.chatsData.get(chatId);
        if (!chatData) {
            chatData = { messages: [], unreadCount: 0, username, chatClient: null, pendingAttachments: []};
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

        this.createChatUI(chatId, username, chatname, withFiles);
        this.renderMessages(chatId);
        this.activeChat = chatId;
        this.showSystemMessage(`✅ Присоединились к чату ${chatId}`, 'success');
    }

    async leaveChat(chatId) {
        const chatData = this.chatsData.get(chatId);
        const container = document.getElementById('chatsContainer');
        if (chatData && chatData.chatClient) chatData.chatClient.disconnect();
        const el = document.getElementById(`chat-${chatId}`);
        if (el) el.remove();
        this.activeChat = null;
        container.style.display = "none";
        this.showSystemMessage(`👋 Чат ${chatId} закрыт`, 'info');
    }

    createChatUI(chatId, username, chatname, withFiles) {
        let prefix = '';
        const iframe = document.querySelector('iframe[src^="/crm/lead/details/"]');
        if (iframe) prefix = 'parent.';
        const container = document.getElementById('chatsContainer');
        const existing = document.getElementById(`chat-${chatId}`);
        if (existing) existing.remove();
        const chatCard = document.createElement('div');
        chatCard.className = 'chat-card';
        chatCard.id = `chat-${chatId}`;
        const element = document.querySelector('[data-chat-id="'+chatId+'"]');
        if (element.classList.contains('inactive')){
            chatCard.innerHTML = `
                <div class="chat-header">
                    <h3>${chatname}</h3>
                    <button class="close-chat" onclick="${prefix}window.chatManager.leaveChat('${chatId}')">✕</button>
                </div>
                <div class="messages-container" id="messages-${chatId}"></div>
            `;
        }else{
            if (withFiles){
                chatCard.innerHTML = `
                    <div class="chat-header">
                        <h3>${chatname}</h3>
                        <button class="close-chat" onclick="${prefix}window.chatManager.leaveChat('${chatId}')">✕</button>
                    </div>
                    <div class="messages-container" id="messages-${chatId}"></div>
                    <div class="chat-input">
                        <input type="text" id="message-input-${chatId}" 
                            placeholder="Наберите сообщение" 
                            onkeypress="${prefix}window.chatManager.handleKeyPress(event, '${chatId}')">
                        <button class="attach-btn" onclick="${prefix}window.chatManager.attachFile('${chatId}')"><i class="fa-solid fa-file-arrow-up"></i></button>
                        <button onclick="${prefix}window.chatManager.sendMessage('${chatId}')">Отправить ➤</button>
                    </div>
                    <div class="attachments-preview" id="attachments-preview-${chatId}"></div>
                    <input type="file" id="file-input-${chatId}" style="display:none" multiple accept="image/*,application/pdf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document">
            `;
            }else{
                chatCard.innerHTML = `
                    <div class="chat-header">
                        <h3>${chatname}</h3>
                        <button class="close-chat" onclick="${prefix}window.chatManager.leaveChat('${chatId}')">✕</button>
                    </div>
                    <div class="messages-container" id="messages-${chatId}"></div>
                    <div class="chat-input">
                        <input type="text" id="message-input-${chatId}" 
                            placeholder="Наберите сообщение" 
                            onkeypress="${prefix}window.chatManager.handleKeyPress(event, '${chatId}')">
                        <button onclick="${prefix}window.chatManager.sendMessage('${chatId}')">Отправить ➤</button>
                    </div>
                `;
            }
        }
        
        
        container.appendChild(chatCard);
        container.style.display = "flex";
        setTimeout(() => document.getElementById(`message-input-${chatId}`)?.focus(), 150);

        // после добавления chatCard в DOM
        const fileInput = document.getElementById(`file-input-${chatId}`);
        if (fileInput) {
            fileInput.addEventListener('change', (e) => {
                if (e.target.files.length > 0) {
                    // Обрабатываем каждый файл по очереди
                    const files = Array.from(e.target.files);
                    this.handleFilesSelected(chatId, files);
                    e.target.value = ''; // сброс
                }
            });
        }
    }

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
            const attachmentsHtml = msg.attachments && msg.attachments.length ? 
                `<div class="message-attachments">
                    ${msg.attachments.map(a => `
                        <div class="attachment-link">
                            <i class="fa-solid fa-file-arrow-up"></i>
                            <a href="${this.escapeHtml(a.file_url)}" target="_blank" download="${this.escapeHtml(a.file_name)}">${this.escapeHtml(a.file_name)}</a>
                        </div>
                    `).join('')}
                </div>` : '';
            return `
                <div class="message ${isOwn ? 'message-own' : 'message-other'}">
                    <div class="message-bubble">
                        <span class="message-username">${this.escapeHtml(msg.user_name)}</span>
                        <div class="message-text">${this.escapeHtml(msg.text)}</div>
                        ${attachmentsHtml}
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

        const attachmentsHtml = message.attachments && message.attachments.length ? 
            `<div class="message-attachments">
                ${message.attachments.map(a => `
                    <div class="attachment-link">
                        <i class="fa-solid fa-file-arrow-up"></i>
                        <a href="${this.escapeHtml(a.file_url)}" target="_blank" download="${this.escapeHtml(a.file_name)}">${this.escapeHtml(a.file_name)}</a>
                    </div>
                `).join('')}
            </div>` : '';

        msgDiv.innerHTML = `
            <div class="message-bubble">
                <span class="message-username">${this.escapeHtml(message.user_name)}</span>
                <div class="message-text">${this.escapeHtml(message.text)}</div>
                ${attachmentsHtml}
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
        const text = input?.value.trim() || '';
        const attachments = chatData.pendingAttachments || [];
        if (!text && attachments.length === 0) return;

        const ok = await chatData.chatClient.sendMessage(text, attachments);
        if (ok) {
            input.value = '';
            chatData.pendingAttachments = [];
            this.renderAttachmentsPreview(chatId);
        } else {
            this.showSystemMessage('❌ Не удалось отправить', 'error');
        }
    }

    updateChatBadge(chatId, count) {
        const  buttons = document.querySelectorAll(`.open-chat[data-chat-id='${chatId}']`);
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

    async handleFilesSelected(chatId, files) {
        const chatData = this.chatsData.get(chatId);
        if (!chatData) return;

        // Проверяем лимит
        const currentCount = chatData.pendingAttachments.length;
        const available = 5 - currentCount;
        if (files.length > available) {
            this.showSystemMessage(`Можно добавить не более ${available} файлов`, 'error');
            files = files.slice(0, available);
        }

        for (const file of files) {
            try {
                const result = await chatData.chatClient.uploadFile(file);
                chatData.pendingAttachments.push({
                    file_url: result.file_url,
                    file_name: result.file_name,
                    // можно сохранить и размер для отображения
                });
            } catch(e) {
                this.showSystemMessage(`Ошибка загрузки файла ${file.name}`, 'error');
            }
        }
        this.renderAttachmentsPreview(chatId);
    }

    renderAttachmentsPreview(chatId) {
        const container = document.getElementById(`attachments-preview-${chatId}`);
        if (!container) return;
        const chatData = this.chatsData.get(chatId);
        if (!chatData || chatData.pendingAttachments.length === 0) {
            container.innerHTML = '';
            container.style.display = 'none';
            return;
        }
        container.style.display = 'flex';
        container.innerHTML = chatData.pendingAttachments.map((att, index) => `
            <div class="attachment-item" title="${this.escapeHtml(att.file_name)}">
                <span class="file-icon"><i class="fa-solid fa-file-arrow-up"></i></span>
                <span class="file-name">${this.escapeHtml(att.file_name)}</span>
                <span class="remove-attachment" data-index="${index}">✕</span>
            </div>
        `).join('');

        // Обработчики удаления
        container.querySelectorAll('.remove-attachment').forEach(el => {
            el.addEventListener('click', () => {
                const idx = parseInt(el.dataset.index);
                chatData.pendingAttachments.splice(idx, 1);
                this.renderAttachmentsPreview(chatId);
            });
        });
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
        const resp = await fetch(`/chat/${this.chatId}/upload`, {
            method: 'POST',
            body: formData
        });
        if (!resp.ok) {
            throw new Error('Upload failed');
        }
        return await resp.json(); // { file_url, file_name, size }
    }

    async loadMessages() {
        const resp = await fetch(`/chat/${this.chatId}`);
        return resp.ok ? await resp.json() : [];
    }
    
    async sendMessage(text, attachments = []) {
        const payload = {
            user_name: this.username,
            text: text,
            attachments: attachments.map(a => ({ file_url: a.file_url, file_name: a.file_name }))
        };
        const resp = await fetch(`/chat/${this.chatId}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        return resp.ok;
    }

    async markAsRead() {
        await fetch(`/chat/${this.chatId}/read`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ user_name: this.username })
        });
    }
    async getUnreadCount() {
        const resp = await fetch(`/chat/${this.chatId}/unread?user_name=${encodeURIComponent(this.username)}`);
        if (!resp.ok) return 0;
        const data = await resp.json();
        return data.unread || 0;
    }

    disconnect() {}
}

document.addEventListener("DOMContentLoaded", async () => {
    const bitrixUserNameContainer = document.getElementById('bitrixUserName');
    const bitrixUserName = bitrixUserNameContainer ? bitrixUserNameContainer.textContent : 'Пользователь';
    window.chatManager = new ChatManager(bitrixUserName);
    await window.chatManager.initialize();
    // Навесить обработчики на кнопки .open-chat
    document.querySelectorAll('.open-chat').forEach(btn => {
        btn.removeEventListener('click', window.joinChat);
        btn.addEventListener('click', window.joinChat);
    });
});

window.joinChat = function(event) {
    const chatId = event.currentTarget.dataset.chatId;
    const withFiles = Boolean(event.currentTarget.dataset.withFiles);
    const chatName = event.currentTarget.dataset.chatName;
    const userName = event.currentTarget.dataset.userName;
    window.chatManager.joinChat(chatId, userName, chatName, withFiles);
};
