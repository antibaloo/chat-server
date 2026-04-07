package main

import (
        "encoding/json"
        "fmt"
        "log"
        "net/http"
        "strconv"
        "strings"
        "sync"
        "time"
)

// Message представляет сообщение в чате
type Message struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Chat представляет комнату чата
type Chat struct {
	ID          int
	Messages    []Message
	mutex       sync.RWMutex
	subscribers map[chan Message]struct{}
	subMutex    sync.RWMutex
}

// ChatManager управляет всеми чатами
type ChatManager struct {
	chats map[int]*Chat
	mutex sync.RWMutex
}

var (
        chatManager = &ChatManager{
        	chats: make(map[int]*Chat),
        }
        messageID = 0
        idMutex   sync.Mutex
)

// getNextMessageID возвращает следующий ID сообщения
func getNextMessageID() int {
	idMutex.Lock()
	defer idMutex.Unlock()
	messageID++
	return messageID
}

// getOrCreateChat получает или создает чат
func getOrCreateChat(chatID int) *Chat {
	chatManager.mutex.Lock()
	defer chatManager.mutex.Unlock()

	chat, exists := chatManager.chats[chatID]
	if !exists {
			chat = &Chat{
					ID:          chatID,
					Messages:    []Message{},
					subscribers: make(map[chan Message]struct{}),
			}
			chatManager.chats[chatID] = chat
	}
	return chat
}

// deleteChat удаляет чат и закрывает все подписки
func deleteChat(chatID int) bool {
	chatManager.mutex.Lock()
	defer chatManager.mutex.Unlock()

	chat, exists := chatManager.chats[chatID]
	if !exists {
			return false
	}

	// Закрываем все каналы подписчиков
	chat.subMutex.Lock()
	for ch := range chat.subscribers {
			close(ch)
	}
	chat.subscribers = make(map[chan Message]struct{})
	chat.subMutex.Unlock()

	// Удаляем чат из менеджера
	delete(chatManager.chats, chatID)

	return true
}

// addMessage добавляет сообщение в чат и уведомляет подписчиков
func (c *Chat) addMessage(username, text string) Message {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	msg := Message{
			ID:        getNextMessageID(),
			Username:  username,
			Text:      text,
			Timestamp: time.Now(),
	}

	c.Messages = append(c.Messages, msg)

	// Уведомляем всех подписчиков
	c.subMutex.RLock()
	for ch := range c.subscribers {
			select {
			case ch <- msg:
			default:
					// Избегаем блокировки
			}
	}
	c.subMutex.RUnlock()

	return msg
}

// getMessages возвращает все сообщения чата
func (c *Chat) getMessages() []Message {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	messages := make([]Message, len(c.Messages))
	copy(messages, c.Messages)
	return messages
}

// subscribe добавляет подписчика на SSE и возвращает канал только для чтения
func (c *Chat) subscribe() <-chan Message {
	ch := make(chan Message, 10)
	c.subMutex.Lock()
	c.subscribers[ch] = struct{}{}
	c.subMutex.Unlock()
	return ch
}

// unsubscribe удаляет подписчика, принимает канал для записи
func (c *Chat) unsubscribe(ch chan Message) {
	c.subMutex.Lock()
	delete(c.subscribers, ch)
	c.subMutex.Unlock()
	close(ch)
}

// CORS middleware
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			if r.Method == "OPTIONS" {
					w.WriteHeader(http.StatusOK)
					return
			}

			next(w, r)
	}
}

// sendMessageHandler обрабатывает POST /chat/{id}/
func sendMessageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
	}

	// Получаем ID чата из URL
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "chat" {
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
	}

	chatID, err := strconv.Atoi(pathParts[1])
	if err != nil {
			http.Error(w, "Invalid chat ID", http.StatusBadRequest)
			return
	}

	// Парсим тело запроса
	var request struct {
			Username string `json:"username"`
			Text     string `json:"text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
	}

	if request.Username == "" || request.Text == "" {
			http.Error(w, "Username and text are required", http.StatusBadRequest)
			return
	}

	// Добавляем сообщение в чат
	chat := getOrCreateChat(chatID)
	msg := chat.addMessage(request.Username, request.Text)

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(msg)
}

// getMessagesHandler обрабатывает GET /chat/{id}/
func getMessagesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
	}

	// Получаем ID чата из URL
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "chat" {
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
	}

	chatID, err := strconv.Atoi(pathParts[1])
	if err != nil {
			http.Error(w, "Invalid chat ID", http.StatusBadRequest)
			return
	}

	// Получаем сообщения
	chat := getOrCreateChat(chatID)
	messages := chat.getMessages()

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// deleteChatHandler обрабатывает DELETE /chat/{id}/
func deleteChatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
	}

	// Получаем ID чата из URL
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "chat" {
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
	}

	chatID, err := strconv.Atoi(pathParts[1])
	if err != nil {
			http.Error(w, "Invalid chat ID", http.StatusBadRequest)
			return
	}

	// Удаляем чат
	if deleted := deleteChat(chatID); !deleted {
			http.Error(w, "Chat not found", http.StatusNotFound)
			return
	}

	// Отправляем успешный ответ
	w.WriteHeader(http.StatusNoContent)
}

// sseHandler обрабатывает GET /chat/{id}/message/
func sseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID чата из URL
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 || pathParts[0] != "chat" || pathParts[2] != "message" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	chatID, err := strconv.Atoi(pathParts[1])
	if err != nil {
		http.Error(w, "Invalid chat ID", http.StatusBadRequest)
		return
	}

	// Настраиваем SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Подписываемся на сообщения
	chat := getOrCreateChat(chatID)
	messageChan := chat.subscribe()

	// Создаем канал для отписки
	done := make(chan struct{})

	// Отложенная отписка
	defer func() {
		close(done)
		// Ждем, пока горутина завершится
		time.Sleep(100 * time.Millisecond)
		// Преобразуем <-chan Message в chan Message для unsubscribe
		writeChan := make(chan Message)
		chat.unsubscribe(writeChan)
	}()

	// Запускаем горутину для перенаправления сообщений
	go func() {
		for {
			select {
			case msg, ok := <-messageChan:
				if !ok {
					// Канал закрыт, выходим
					return
				}
				select {
				case <-done:
					return
				default:
					// Отправляем сообщение клиенту
					data, err := json.Marshal(msg)
					if err != nil {
							continue
					}
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
					}
			case <-done:
				return
			}
		}
	}()

	// Отправляем keep-alive каждые 15 секунд
	keepAliveTicker := time.NewTicker(15 * time.Second)
	defer keepAliveTicker.Stop()

	for {
		select {
		case <-keepAliveTicker.C:
			// Отправляем keep-alive
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			// Клиент отключился
			return
		}
	}
}

func globalSSEHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Настраиваем SSE
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("Access-Control-Allow-Origin", "*")

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "SSE not supported", http.StatusInternalServerError)
        return
    }

    // Создаем канал для глобальных сообщений
    globalChan := make(chan Message, 100)

    // Регистрируем глобального подписчика во всех чатах
    chatManager.mutex.RLock()
    for _, chat := range chatManager.chats {
        chat.subMutex.Lock()
        chat.subscribers[globalChan] = struct{}{}
        chat.subMutex.Unlock()
    }
    chatManager.mutex.RUnlock()

    // Функция для удаления подписчика
    defer func() {
        chatManager.mutex.RLock()
        for _, chat := range chatManager.chats {
            chat.subMutex.Lock()
            delete(chat.subscribers, globalChan)
            chat.subMutex.Unlock()
        }
        chatManager.mutex.RUnlock()
        close(globalChan)
    }()

    // Отправляем keep-alive каждые 15 секунд
    keepAliveTicker := time.NewTicker(15 * time.Second)
    defer keepAliveTicker.Stop()

    for {
        select {
        case msg, ok := <-globalChan:
            if !ok {
                return
            }

            // Определяем ID чата из сообщения
            chatID := 0
            chatManager.mutex.RLock()
            for id, chat := range chatManager.chats {
                chat.mutex.RLock()
                for _, m := range chat.Messages {
                    if m.ID == msg.ID {
                        chatID = id
                        break
                    }
                }
                chat.mutex.RUnlock()
                if chatID != 0 {
                    break
                }
            }
            chatManager.mutex.RUnlock()

            // Отправляем сообщение с ID чата
            data := struct {
                ChatID  int     `json:"chatId"`
                Message Message `json:"message"`
            }{
                ChatID:  chatID,
                Message: msg,
            }

            jsonData, err := json.Marshal(data)
            if err != nil {
                continue
            }

            fmt.Fprintf(w, "data: %s\n\n", jsonData)
            flusher.Flush()

        case <-keepAliveTicker.C:
            fmt.Fprintf(w, ": keepalive\n\n")
            flusher.Flush()

        case <-r.Context().Done():
            return
        }
    }
}

func main() {
	// API endpoints
	http.HandleFunc("/chat/global/messages/", corsMiddleware(globalSSEHandler))
	http.HandleFunc("/chat/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")

		// Проверяем, это запрос к SSE или нет
		if strings.HasSuffix(path, "/message") {
			corsMiddleware(sseHandler)(w, r)
		} else if r.Method == http.MethodPost {
			corsMiddleware(sendMessageHandler)(w, r)
		} else if r.Method == http.MethodGet {
			corsMiddleware(getMessagesHandler)(w, r)
		} else if r.Method == http.MethodDelete {
			corsMiddleware(deleteChatHandler)(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})


	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}