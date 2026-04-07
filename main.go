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
	subMutex    sync.RWMutex
}

// ChatManager управляет всеми чатами
type ChatManager struct {
	chats map[int]*Chat
	subscribers map[chan Message]struct{}
	mutex sync.RWMutex
}

var (
        chatManager = &ChatManager{
        	chats: make(map[int]*Chat),
			subscribers: make(map[chan Message]struct{}),
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
			}
			chatManager.chats[chatID] = chat
	}
	return chat
}

// deleteChat удаляет чат и закрывает все подписки
func deleteChat(chatID int) bool {
	chatManager.mutex.Lock()
	defer chatManager.mutex.Unlock()

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
	chatManager.mutex.RLock()
	for ch := range chatManager.subscribers {
			select {
			case ch <- msg:
				fmt.Println("Сообщение отправлено в канал подписчика")
			default:
					// Избегаем блокировки
			}
	}
	chatManager.mutex.RUnlock()

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


// sendMessageHandler обрабатывает OPTIONS /chat/{id}/
func optionsHandler(w http.ResponseWriter, r *http.Request) {}

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
	fmt.Println("Создан канал подписчика")
    // Регистрируем глобального подписчика во всех чатах
    chatManager.mutex.RLock()

	chatManager.subscribers[globalChan] = struct{}{}
	fmt.Printf("Канал подписчика добавлен, всего подписчиков: %v\n", len(chatManager.subscribers))
    
    chatManager.mutex.RUnlock()

    // Функция для удаления подписчика
    defer func() {
        chatManager.mutex.RLock()
        delete(chatManager.subscribers, globalChan)
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
			fmt.Println(msg)
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
		switch (r.Method) {
			case http.MethodPost:
				corsMiddleware(sendMessageHandler)(w, r)
			case http.MethodGet:
				corsMiddleware(getMessagesHandler)(w, r)
			case http.MethodDelete:
				corsMiddleware(deleteChatHandler)(w, r)
			case http.MethodOptions:
				corsMiddleware(optionsHandler)(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})


	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}