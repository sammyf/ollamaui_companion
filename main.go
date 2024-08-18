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
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// LLMAnswer
type LLMAnswer struct {
	Model              string   `json:"model"`
	CreatedAt          string   `json:"created_at"`
	Message            Messages `json:"message"`
	Done               bool     `json:"done"`
	TotalDuration      int      `json:"total_duration"`
	LoadDuration       int      `json:"load_duration"`
	PromptEvalCount    int      `json:"prompt_eval_count"`
	PromptEvalDuration int      `json:"prompt_eval_duration"`
	EvalCount          int      `json:"eval_count"`
	EvalDuration       int      `json:"eval_duration"`
}

// Messages
type Messages struct {
	Id      int    `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Persona string `json:"persona"`
}

type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResult struct {
	Result    bool   `json:"result"`
	CsrfToken string `json:"csrf_token"`
}

// var db *sql.DB

type Payload struct {
	// Define the fields of your payload here
	// Example:
	// Message string `json:"message"`
}

// Handler for haproxy Healthcheck endpoint
func healthChkHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("still alive"))
	return
}

/////////////////////////////////////////////////////////////
// Handler for Ollama interactions
/////////////////////////////////////////////////////////////

// Handler for the /async/chat endpoint
func chatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var payload Payload
	err = json.Unmarshal(body, &payload)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	uniqueID := uuid.New().String()

	fmt.Println("uniqueId: " + uniqueID)

	db, _ := getDb()
	_, err = db.Exec("INSERT INTO async (uuid, prompt, answer) VALUES (?, ?, 'still processing')", uniqueID, string(body))
	defer db.Close()
	if err != nil {
		log.Printf("Failed to insert data into MariaDB database: %v", err)
	}
	fmt.Println("Sending Response to Ollama")
	go func() {
		asyncRequest(uniqueID, body)
	}()

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte(`{"uniqueID":"` + uniqueID + `"}`))
	if err != nil {
		return
	}
}

func responseHandler(w http.ResponseWriter, r *http.Request) {

	uid := r.URL.Query().Get("uid") // Assuming /companion/response?uid=<uid> as Go's http package doesn't handle URL parameters directly

	// Fetch the answer from the queue table
	var sqlAnswer string
	db, _ := getDb()
	defer db.Close()
	err := db.QueryRow("SELECT answer FROM async WHERE uuid = ?", uid).Scan(&sqlAnswer)
	if err != nil {
		if err == sql.ErrNoRows {
			notFoundMsg := LLMAnswer{Model: "not found"}
			jsonRes, _ := json.Marshal(notFoundMsg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(jsonRes)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// If the answer is still processing
	if sqlAnswer == "" {
		stillProcessingMsg := LLMAnswer{Model: "still processing"}
		jsonRes, _ := json.Marshal(stillProcessingMsg)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonRes)
		return
	}

	var answer map[string]interface{}
	err = json.Unmarshal([]byte(sqlAnswer), &answer)
	if err != nil {
		stillProcessingMsg := LLMAnswer{Model: "still processing"}
		jsonRes, _ := json.Marshal(stillProcessingMsg)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonRes)
		return
	}

	// Delete the entry from the queue
	_, err = db.Exec("DELETE FROM async WHERE uuid = ?", uid)
	db.Close()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Return the answer
	jsonRes, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonRes)
}

func unloadHandler(w http.ResponseWriter, r *http.Request) {

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()
	// Create custom HTTP client with a 10-minute timeout
	client := &http.Client{
		Timeout: 1,
	}

	// Create a new request
	req, err := http.NewRequest("GET", "http://ollama.local:11111/api/chat", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("Failed to create new request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to make Cleanup request to external service: %v", err)
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(http.StatusOK)
}

func tagsHandler(w http.ResponseWriter, r *http.Request) {

	// Create custom HTTP client with a 10-minute timeout
	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	// Create a new request
	req, err := http.NewRequest("GET", "http://ollama.local:11111/api/tags", nil)
	if err != nil {
		log.Printf("Failed to create new request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to make PS request to external service: %v", err)
		return
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response from external service: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(responseBody)
}

func psHandler(w http.ResponseWriter, r *http.Request) {

	// Create custom HTTP client with a 10-minute timeout
	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	// Create a new request
	req, err := http.NewRequest("GET", "http://ollama.local:11111/api/ps", nil)
	if err != nil {
		log.Printf("Failed to create new request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to make PS request to external service: %v", err)
		return
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response from external service: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(responseBody)
}

// Perform asynchronous request and store result in the database
func asyncRequest(uuid string, requestBody []byte) {

	// Create custom HTTP client with a 10-minute timeout
	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	// Create a new request
	req, err := http.NewRequest("POST", "http://ollama.local:11111/api/chat", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("Failed to create new request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to make request to external service: %v", err)
		return
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response from external service: %v", err)
		return
	}

	db, _ := getDb()
	_, err = db.Exec("UPDATE async SET answer = ? WHERE uuid=?", string(responseBody), uuid)
	db.Close()
	if err != nil {
		log.Printf("Failed to insert data into SQLite database: %v", err)
	}
}

/////////////////////////////////////////////////////////////
// Handler for Chatlog and Memory Management
/////////////////////////////////////////////////////////////

func storeChatLogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	fmt.Println("body : ", string(body))

	var messages Messages
	err = json.Unmarshal(body, &messages)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	now := time.Now()

	userId, err := getUserId(w, r)
	if err != nil {
		return
	}
	db, _ := getDb()
	defer db.Close()
	_, err = db.Exec("INSERT INTO chat_log (user_id, persona, role, content, datetime) VALUES (?,?,?,?,?)", userId, messages.Persona, messages.Role, messages.Content, now)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
	return
}

func getChatLogHandler(w http.ResponseWriter, r *http.Request) {
	var messages []Messages
	db, err := getDb()
	defer db.Close()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	userId, err := getUserId(w, r)
	if err != nil {
		return
	}
	rows, err := db.Query("SELECT id, persona, role, content FROM chat_log WHERE user_id = ? ORDER BY datetime", userId)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var msg Messages
		err = rows.Scan(&msg.Id, &msg.Persona, &msg.Role, &msg.Content)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		messages = append(messages, msg)
	}

	if len(messages) == 0 {
		messages = append(messages, Messages{Id: 0, Persona: "nobody", Role: "user", Content: "nothing to show"})
	}

	jsonRes, err := json.Marshal(messages)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonRes)
}

/////////////////////////////////////////////////////////////
// Handler for Login and User Management
/////////////////////////////////////////////////////////////

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var login Login
	err = json.Unmarshal(body, &login)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	db, _ := getDb()
	var userid int
	err = db.QueryRow("select id FROM users WHERE username=? AND password=?", login.Username, login.Password).Scan(&userid)
	if err != nil {
		if err == sql.ErrNoRows {
			lr := LoginResult{Result: false, CsrfToken: ""}
			rs, _ := json.Marshal(lr)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(rs)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		db.Close()
		return
	}
	csrfToken := uuid.New().String()
	_, err = db.Exec("UPDATE users SET csrf=? WHERE id=?", csrfToken, userid)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
	db.Close()
	lr := LoginResult{Result: true, CsrfToken: csrfToken}
	rs, _ := json.Marshal(lr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(rs)
	return
}

var dsn string

func init() {
	var err error
	dsn, err = buildDSN()
	if err != nil {
		log.Fatalf("Error building DSN: %v", err)
	}
}

func getUserId(w http.ResponseWriter, r *http.Request) (int, error) {
	csrfToken := r.Header.Get("X-CSRF-TOKEN")

	db, _ := getDb()
	var userid int
	err := db.QueryRow("select id FROM users WHERE csrf=?", csrfToken).Scan(&userid)
	if err != nil {
		if err == sql.ErrNoRows {
			lr := LoginResult{Result: false, CsrfToken: ""}
			rs, _ := json.Marshal(lr)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(rs)
			return -1, err
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return -1, err
		}
		db.Close()
	}
	return userid, nil
}

func buildDSN() (string, error) {
	user := os.Getenv("DB_USER")
	if user == "" {
		return "", fmt.Errorf("DB_USER environment variable not set")
	}

	password := os.Getenv("DB_PASSWORD")
	if password == "" {
		return "", fmt.Errorf("DB_PASSWORD environment variable not set")
	}

	host := os.Getenv("DB_HOST")
	if host == "" {
		return "", fmt.Errorf("DB_HOST environment variable not set")
	}

	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		return "", fmt.Errorf("DB_NAME environment variable not set")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s", user, password, host, dbName)
	return dsn, nil
}

func getDb() (*sql.DB, error) {
	// Open a connection to the database
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}

	// Verify the connection to the database
	err = db.Ping()
	if err != nil {
		log.Fatalf("Error connecting to the database: %v", err)
	}
	return db, nil
}

func main() {
	fmt.Println("Listening on port 32225")
	http.HandleFunc("/async/chat", chatHandler)
	http.HandleFunc("/", healthChkHandler)
	http.HandleFunc("/async/response", responseHandler)
	http.HandleFunc("/async/ps", psHandler)
	http.HandleFunc("/async/tags", tagsHandler)
	http.HandleFunc("/async/unload", unloadHandler)
	http.HandleFunc("/async/storeChatLog", storeChatLogHandler)
	http.HandleFunc("/async/getChatLog", getChatLogHandler)
	http.HandleFunc("/async/login", loginHandler)
	log.Fatal(http.ListenAndServe("0.0.0.0:32225", nil))
}
