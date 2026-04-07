package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Message представляет сообщение в чате
type Message struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Chat представляет комнату чата
type Chat struct {
	ID       int
	Messages []Message
	mutex    sync.RWMutex
}

// ChatManager управляет всеми чатами
type ChatManager struct {
	chats       map[int]*Chat
	subscribers map[chan Message]struct{}
	mutex       sync.RWMutex
}

var (
	chatManager = &ChatManager{
		chats:       make(map[int]*Chat),
		subscribers: make(map[chan Message]struct{}),
	}
	db        *sql.DB
)

// Подключаемся к БД
func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "./chat-server.db")
	if err != nil {
		return err
	}
	return nil
}

// Сохраняем сообщение в БД
func saveMessageToDB(msg Message, chatID int) (int64, error) {
	res, err := db.Exec(`
        INSERT INTO messages(chat_id, user_name, text, created_at)
        VALUES(?, ?, ?, ?)
    `, chatID, msg.Username, msg.Text, msg.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Читпем сообщения из БД при загрузке
func loadMessagesFromDB() (map[int][]Message, error) {
	rows, err := db.Query(`
		SELECT id, chat_id, text, user_name, created_at 
		FROM messages ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messagesByChat := make(map[int][]Message)
	for rows.Next() {
		var msg Message
		var chatID int
		var createdAtStr string
		err = rows.Scan(&msg.ID, &chatID, &msg.Text, &msg.Username, &createdAtStr)
		if err != nil {
			return nil, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		messagesByChat[chatID] = append(messagesByChat[chatID], msg)
	}
	return messagesByChat, nil
}


// getOrCreateChat получает или создает чат
func getOrCreateChat(chatID int) *Chat {
	chatManager.mutex.Lock()
	defer chatManager.mutex.Unlock()

	chat, exists := chatManager.chats[chatID]
	if !exists {
		chat = &Chat{
			ID:       chatID,
			Messages: []Message{},
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
func (c *Chat) addMessage(username, text string) (Message, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	msg := Message{
		Username:  username,
		Text:      text,
		CreatedAt: time.Now(),
	}
	id, err := saveMessageToDB(msg, c.ID)
	if err != nil {
		return msg, err
	}

	msg.ID = id
	c.Messages = append(c.Messages, msg)

	// Уведомляем всех подписчиков
	chatManager.mutex.RLock()
	for ch := range chatManager.subscribers {
		select {
		case ch <- msg:
		default:
			// Избегаем блокировки
		}
	}
	chatManager.mutex.RUnlock()

	return msg, nil
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
	msg, err := chat.addMessage(request.Username, request.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	// Регистрируем глобального подписчика во всех чатах
	chatManager.mutex.RLock()

	chatManager.subscribers[globalChan] = struct{}{}

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
	err := initDB()
	if err != nil {
		log.Fatal(err)
	}

	messagesByChat, err := loadMessagesFromDB()
	if err != nil {
		log.Fatal("Load messages:", err)
	}
	for chatID, msgs := range messagesByChat {
		chat := &Chat{
			ID:       chatID,
			Messages: msgs,
		}
		chatManager.mutex.Lock()
		chatManager.chats[chatID] = chat
		chatManager.mutex.Unlock()
	}

	// API endpoints
	http.HandleFunc("/chat/global/messages/", corsMiddleware(globalSSEHandler))
	http.HandleFunc("/chat/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
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
