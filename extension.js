// Инициализация при загрузке страницы
document.addEventListener("DOMContentLoaded", async () => {
    if (!parent.window.globalNotifications) {
        class GlobalNotifications {
            constructor() {
                this.eventSource = null;
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
                    this.eventSource = new EventSource('https://bx24.hwdev.ru/chat/global/messages/');
                    
                    this.eventSource.onopen = () => {
                        console.log('✅ Глобальное SSE соединение для уведомлений менеджеров установлено.');
                        this.isConnecting = false;
                        this.reconnectAttempts = 0;
                    };
                    
                    this.eventSource.onmessage = (event) => {
                        try {
                            const data = JSON.parse(event.data);
                            console.log(data);
                            // BX.ajax.runAction('meko:partner.Chat.getUserAndNotify', {
                            //     data: {
                            //         miscountId: data.chatId,
                            //         message: data.message.text,
                            //         messageId: data.message.id,
                            //         userName: data.message.username,
                            //     }
                            // }).then(function(response) {
                            //     console.log('Успех:', response.data);
                            // }).catch(function(response) {
                            //     console.error('Ошибка:', response.errors);
                            // });
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
        window.globalNotifications = new GlobalNotifications();
        await window.globalNotifications.initialize();
    }
});