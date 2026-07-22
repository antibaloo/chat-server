package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const authToken = "e-&5j(f-xZ<nfb5s=Om:#PZyYi*YJxP&4?E^<Xt0tgXPex-PLG@xaM:Dn)Y&k}{Ye<n*-7eL^5iQB!<AR!MUku{ccs:t+vXuDw$6S"

type AuthRequest struct {
	Token string `json:"token"` // поле token в JSON
}

type Attachment struct {
	FileURL  string `json:"file_url"`
	FileName string `json:"file_name"`
}

// Message представляет сообщение в чате
type Message struct {
	ID          int64        `json:"id"`
	Username    string       `json:"user_name"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// Chat представляет комнату чата
type Chat struct {
	ID       string
	Messages []Message
	mutex    sync.RWMutex
}

// ChatManager управляет всеми чатами
type ChatManager struct {
	chats       map[string]*Chat
	subscribers map[chan Message]struct{}
	mutex       sync.RWMutex
}

var (
	chatManager = &ChatManager{
		chats:       make(map[string]*Chat),
		subscribers: make(map[chan Message]struct{}),
	}
	db *sql.DB
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
func saveMessageToDB(msg Message, chatID string) (int64, error) {
	var attachmentsJSON interface{}
	if len(msg.Attachments) > 0 {
		data, err := json.Marshal(msg.Attachments)
		if err != nil {
			return 0, err
		}
		attachmentsJSON = string(data)
	} else {
		attachmentsJSON = nil
	}

	res, err := db.Exec(`
        INSERT INTO messages(chat_id, user_name, text, attachments, created_at)
        VALUES(?, ?, ?, ?, ?)
    `, chatID, msg.Username, msg.Text, attachmentsJSON, msg.CreatedAt.Format(time.RFC3339))
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
func loadMessagesFromDB() (map[string][]Message, error) {
	rows, err := db.Query(`
		SELECT id, chat_id, text, user_name, attachments, created_at 
		FROM messages ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messagesByChat := make(map[string][]Message)
	for rows.Next() {
		var msg Message
		var chatID string
		var createdAtStr string
		var attachmentsStr sql.NullString

		err = rows.Scan(&msg.ID, &chatID, &msg.Text, &msg.Username, &attachmentsStr, &createdAtStr)
		if err != nil {
			return nil, err
		}

		if attachmentsStr.Valid && attachmentsStr.String != "" {
			if err := json.Unmarshal([]byte(attachmentsStr.String), &msg.Attachments); err != nil {
				// Логируем ошибку, но не прерываем загрузку
				log.Printf("Failed to parse attachments for message %d: %v", msg.ID, err)
			}
		}

		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		messagesByChat[chatID] = append(messagesByChat[chatID], msg)
	}
	return messagesByChat, nil
}

// Удаляем чат из БД
func deleteChatFromDB(chatID string) error {
	_, err := db.Exec("DELETE FROM messeges WHERE chat_id = ?", chatID)
	return err
}

// Метим сообщения как прочитанные в БД
func markMessagesAsReadInDB(chatID, Username string) error {
	// Отмечаем все сообщения в чате, которые не принадлежат пользователю
	_, err := db.Exec(`
		INSERT OR IGNORE INTO read_receipts(message_id, user_name, read_at)
		SELECT m.id, ?, ?
		FROM messages m
		WHERE m.chat_id = ? AND m.user_name != ?
	`, Username, time.Now().Format(time.RFC3339), chatID, Username)
	return err
}

// Получаем количество непрочитанных сообщений в чате для пользователя из БД
func getUnreadCountFromDB(chatID, Username string) (int, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM messages m
		LEFT JOIN read_receipts rr ON m.id = rr.message_id AND rr.user_name = ?
		WHERE m.chat_id = ? AND m.user_name != ? AND rr.message_id IS NULL
	`, Username, chatID, Username).Scan(&count)
	return count, err
}

// getOrCreateChat получает или создает чат
func getOrCreateChat(chatID string) *Chat {
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
func deleteChat(chatID string) bool {
	chatManager.mutex.Lock()
	defer chatManager.mutex.Unlock()

	if err := deleteChatFromDB(chatID); err != nil {
		log.Printf("Failed to delete chat %s from DB: %v", chatID, err)
	}
	// Удаляем чат из менеджера
	delete(chatManager.chats, chatID)

	return true
}

// addMessage добавляет сообщение в чат и уведомляет подписчиков
func (c *Chat) addMessage(username, text string, attachments []Attachment) (Message, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	msg := Message{
		Username:    username,
		Text:        text,
		Attachments: attachments,
		CreatedAt:   time.Now(),
	}

	id, err := saveMessageToDB(msg, c.ID)
	if err != nil {
		return msg, err
	}

	msg.ID = id
	c.Messages = append(c.Messages, msg)

	// Уведомляем всех подписчиков
	chatManager.mutex.Lock()
	for ch := range chatManager.subscribers {
		select {
		case ch <- msg:
		default:
			// Избегаем блокировки
		}
	}
	chatManager.mutex.Unlock()

	return msg, nil
}

// Отмечаем как прочитанные все сообщения в чате
func (c *Chat) MarkMessagesAsRead(Username string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return markMessagesAsReadInDB(c.ID, Username)
}

// Получаем кол-во непрочитанных сообщений в чате
func (c *Chat) GetUnreadCount(Username string) (int, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return getUnreadCountFromDB(c.ID, Username)
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

	chatID := pathParts[1]

	// Парсим тело запроса
	var request struct {
		Username    string       `json:"user_name"`
		Text        string       `json:"text"`
		Attachments []Attachment `json:"attachments,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if request.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	if len(request.Attachments) == 0 && request.Text == "" {
		http.Error(w, "Text or attachments are required", http.StatusBadRequest)
		return
	}

	if len(request.Attachments) > 0 && request.Text == "" {
		request.Text = "(только вложения)"
	}

	// Ограничение количества файлов (можно на клиенте, но и на сервере проверить)
	if len(request.Attachments) > 5 {
		http.Error(w, "Maximum 5 attachments per message", http.StatusBadRequest)
		return
	}

	// Добавляем сообщение в чат
	chat := getOrCreateChat(chatID)
	msg, err := chat.addMessage(request.Username, request.Text, request.Attachments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	//отправляем запрос к API Хайвей для обновления элемента, к которому привязан чат, и обновлению грида АРМ сертификации
	url := "https://bx24.hwdev.ru/api/newChatMessage/" + extractDigitsRegex(chatID) + "/" // ваш адрес

	// Создаём экземпляр с нужным токеном
	reqData := AuthRequest{
		Token: authToken,
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Println("Ошибка создания запроса:", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Ошибка выполнения запроса:", err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = body

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(msg)
}

func extractDigitsRegex(s string) string {
	re := regexp.MustCompile(`[^0-9]`)
	return re.ReplaceAllString(s, "")
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

	chatID := pathParts[1]

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

	chatID := pathParts[1]

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
	chatManager.mutex.Lock()

	chatManager.subscribers[globalChan] = struct{}{}

	chatManager.mutex.Unlock()

	// Функция для удаления подписчика
	defer func() {
		chatManager.mutex.Lock()
		delete(chatManager.subscribers, globalChan)
		close(globalChan)
		chatManager.mutex.Unlock()
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
			chatID := ""
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
				if chatID != "" {
					break
				}
			}
			chatManager.mutex.RUnlock()

			// Отправляем сообщение с ID чата
			data := struct {
				ChatID  string  `json:"chatId"`
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

// markReadHandler POST /chat/{id}/read
func markReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// извлечение chatID аналогично другим хендлерам
	chatID, err := getChatIDFromPath(r.URL.Path)
	if err != nil {
		http.Error(w, "Invalid chat ID", http.StatusBadRequest)
		return
	}
	var req struct {
		Username string `json:"user_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		http.Error(w, "Username required", http.StatusBadRequest)
		return
	}
	chat := getOrCreateChat(chatID)
	if err := chat.MarkMessagesAsRead(req.Username); err != nil {
		http.Error(w, "Failed to mark read", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// unreadHandler GET /chat/{id}/unread?user_name=xxx
func unreadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	chatID, err := getChatIDFromPath(r.URL.Path)
	if err != nil {
		http.Error(w, "Invalid chat ID", http.StatusBadRequest)
		return
	}
	Username := r.URL.Query().Get("user_name")
	if Username == "" {
		http.Error(w, "Username required", http.StatusBadRequest)
		return
	}
	chat := getOrCreateChat(chatID)
	count, err := chat.GetUnreadCount(Username)
	if err != nil {
		http.Error(w, "Failed to get unread count", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"unread": count})
}

// uploadFileHandler POST /chat/{id}/upload
func uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	chatID, err := getChatIDFromPath(r.URL.Path)
	if err != nil {
		http.Error(w, "Invalid chat ID", http.StatusBadRequest)
		return
	}

	// Проверяем/создаём чат (можно просто проверить, что он существует, но create не повредит)
	chat := getOrCreateChat(chatID) // используем chatID, чтобы чат был в памяти
	_ = chat                        // если нужно, можно использовать для других целей

	// Парсим multipart form (максимум 32 MB)
	err = r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Проверка размера (дополнительно)
	if header.Size > 20*1024*1024 { // 20 MB
		http.Error(w, "File too large (max 20MB)", http.StatusBadRequest)
		return
	}

	// Создаём папку для чата, если её нет
	chatDir := fmt.Sprintf("chat_%s", chatID)
	if err := os.MkdirAll("uploads/"+chatDir, 0755); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Генерация уникального имени
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".bin"
	}
	newName := uuid.New().String() + ext
	savePath := filepath.Join("uploads/"+chatDir, newName)

	// Сохраняем файл
	out, err := os.Create(savePath)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Формируем URL для доступа к файлу (можно использовать абсолютный URL)
	fileURL := "https://chat.keypartners24.ru/chat/uploads/file/" + chatDir + "/" + newName
	// Если сервер доступен по домену, лучше вернуть полный URL:
	// fileURL = "https://bx24.hwdev.ru/uploads/" + newName

	// Возвращаем JSON с информацией о файле
	resp := map[string]interface{}{
		"file_url":  fileURL,
		"file_name": header.Filename,
		"size":      header.Size,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Определить ID чата из пути
func getChatIDFromPath(path string) (string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "chat" {
		return "", fmt.Errorf("invalid path")
	}
	return parts[1], nil
}

func serveFileHandler(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL.Path)
	// Удаляем префикс /chat/uploads/
	path := strings.TrimPrefix(r.URL.Path, "/chat/uploads/file/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	// Безопасность: запрещаем выход за пределы папки
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	log.Print(filepath.Join("./uploads", path))
	http.ServeFile(w, r, filepath.Join("./uploads", path))
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
	router := http.NewServeMux()
	// API endpoints
	router.HandleFunc("GET /chat/global/messages", corsMiddleware(globalSSEHandler))
	router.HandleFunc("GET /chat/{id}/unread", corsMiddleware(unreadHandler))
	router.HandleFunc("POST /chat/{id}/read", corsMiddleware(markReadHandler))
	router.HandleFunc("OPTIONS /chat/{id}/read", corsMiddleware(optionsHandler))
	router.HandleFunc("POST /chat/{id}/upload", corsMiddleware(uploadFileHandler))
	router.HandleFunc("OPTIONS /chat/{id}/upload", corsMiddleware(optionsHandler))
	router.HandleFunc("GET /chat/{id}", corsMiddleware(getMessagesHandler))
	router.HandleFunc("POST /chat/{id}", corsMiddleware(sendMessageHandler))
	router.HandleFunc("OPTIONS /chat/{id}", corsMiddleware(optionsHandler))
	router.HandleFunc("DELETE /chat/{id}", corsMiddleware(deleteChatHandler))
	router.HandleFunc("GET /chat/uploads/file/", corsMiddleware(serveFileHandler))
	router.HandleFunc("HEAD /chat/uploads/file/", corsMiddleware(serveFileHandler))

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}
