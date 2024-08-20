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

const memoryThreshold = 5

type LLMRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Suffix  string `json:"suffix"`
	Options struct {
		Temperature float64 `json:"temperature"`
	}
	Stream bool `json:"stream"`
}

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

// LLMGenerateAnswer
type LLMGenerateAnswer struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response"`
	Done               bool      `json:"done"`
	Context            []int     `json:"context"`
	TotalDuration      int64     `json:"total_duration"`
	LoadDuration       int64     `json:"load_duration"`
	PromptEvalCount    int       `json:"prompt_eval_count"`
	PromptEvalDuration int       `json:"prompt_eval_duration"`
	EvalCount          int       `json:"eval_count"`
	EvalDuration       int64     `json:"eval_duration"`
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

type memoryRequestStruct struct {
	User_id           int `json:"user_id"`
	First_chat_log_id int `json:"first_chat_log_id"`
	Last_chat_log_id  int `json:"last_chat_log_id"`
}

type PsModelDetail struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type PsModel struct {
	Name      string         `json:"name"`
	Model     string         `json:"model"`
	Size      int            `json:"size"`
	Digest    string         `json:"digest"`
	Details   *PsModelDetail `json:"details"`
	ExpiresAt string         `json:"expires_at"`
	SizeVram  int            `json:"size_vram"`
}

type PsModelsData struct {
	Models []PsModel `json:"models"`
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
	go func() {
		asyncChatRequest(uniqueID, body)
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
	// Create custom HTTP client with a 2 seconds timeout
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
	var currentModelList PsModelsData
	err = json.Unmarshal(responseBody, &currentModelList)
	if err != nil {
		log.Printf("Failed to unmarshal model list response: %v", err)
		return
	}

	summarizer := os.Getenv("SUMMARIZER")
	foundAlternative := false
	var alternativeName string

	for _, model := range currentModelList.Models {
		if model.Name != summarizer {
			alternativeName = model.Name
			foundAlternative = true
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")

	if len(currentModelList.Models) == 0 {
		// No models found
		w.Write([]byte(`"none"`))
	} else if foundAlternative {
		// Found an alternative model name
		w.Write([]byte(`"` + alternativeName + `"`))
	} else {
		ewrtew
		// All model names match the summarizer
		w.Write([]byte(`"` + summarizer + `"`))
	}
}

// Perform asynchronous request and store result in the database
func asyncChatRequest(uuid string, requestBody []byte) {

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

func generateChatSegment(uid int) (int, int, string) {
	db, err := getDb()
	if err != nil {
		log.Printf("Failed to open database: %v", err)
		return -1, -1, ""
	}
	defer db.Close()

	var username string
	err = db.QueryRow("SELECT username FROM users WHERE id = ?", uid).Scan(&username)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return -1, -1, ""
	}

	query := "SELECT id, persona, role, content FROM chat_log WHERE is_summarized = false AND user_id = ? ORDER BY id"
	rows, err := db.Query(query, uid)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return -1, -1, ""
	}
	defer rows.Close()

	segment := ""
	count := -1
	var firstID, lastID int
	var id int
	var persona, role, content string

	for rows.Next() {
		err := rows.Scan(&id, &persona, &role, &content)
		if err != nil {
			log.Printf("Failed to execute query (2): %v", err)
			return -1, -1, ""
		}

		if role == "system" {
			continue
		}
		if role == "user" {
			segment += fmt.Sprintf("%s said '\n%s\n'\n\n", username, content)
			lastID = id
			count++
		} else if role == "assistant" {
			segment += fmt.Sprintf("%s said '\n%s\n'\n\n", persona, content)
			lastID = id
			count++
		}
		if count == 0 {
			firstID = id
		}
	}

	err = rows.Err()
	if err != nil {
		return -1, -1, ""
	}

	if count < memoryThreshold {
		return -1, -1, ""
	}

	return firstID, lastID, segment
}

func generateSummary(uid int) {
	firstId, lastId, chatSection := generateChatSegment(uid)
	llmRequest := LLMRequest{
		Model:  os.Getenv("SUMMARIZER"),
		Prompt: chatSection + "\nWrite a summary of the discussion written above.",
		Options: struct {
			Temperature float64 `json:"temperature"`
		}{
			Temperature: 1.0,
		},
		Stream: false,
	}
	options := memoryRequestStruct{
		User_id:           uid,
		First_chat_log_id: firstId,
		Last_chat_log_id:  lastId,
	}
	body, err := json.Marshal(llmRequest)
	if err != nil {
		log.Printf("Failed to generate summary: %v", err)
		return
	}
	go asyncSummaryRequest(options, body)
}

func generateMemoriesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, _ := getUserId(w, r)

	generateSummary(uid)
	w.WriteHeader(http.StatusOK)
}

// Perform asynchronous summary request and store it as memory in the database
func asyncSummaryRequest(requestDetails memoryRequestStruct, requestBody []byte) {

	summary, err := callGenerateOnSummarizer(requestBody)
	if err != nil {
		log.Printf("Failed to generate summary: %v", err)
		return
	}

	llmRequest := LLMRequest{
		Model:  os.Getenv("SUMMARIZER"),
		Prompt: summary + "\ngive me 10 semi-colon separated keywords for the previous text",
		Options: struct {
			Temperature float64 `json:"temperature"`
		}{
			Temperature: 1.0,
		},
		Stream: false,
	}

	requestBody, err = json.Marshal(llmRequest)
	if err != nil {
		log.Printf("Failed to generate keywords: %v", err)
		return
	}

	keywords, err := callGenerateOnSummarizer(requestBody)
	if err != nil {
		log.Printf("Failed to generate keywords: %v", err)
		return
	}

	if os.Getenv("DEBUG") == "1" {
		fmt.Println("\n\nSUMMARY\n" + summary + "\nKEYWORDS:\n" + keywords + "\n\n")
	} else {
		db, _ := getDb()
		_, err = db.Exec("INSERT INTO memories (user_id, first_chat_log_id, last_chat_log_id, content, keywords)  VALUES (?, ?, ?, ?, ?)", requestDetails.User_id, requestDetails.First_chat_log_id, requestDetails.Last_chat_log_id, summary, keywords)
		_, err = db.Exec("UPDATE chat_log SET is_summarized=true WHERE id>=? AND id <=?", requestDetails.First_chat_log_id, requestDetails.Last_chat_log_id)
		db.Close()
		if err != nil {
			log.Printf("Failed to insert data into SQLite database: %v", err)
		}
	}
}

func callGenerateOnSummarizer(requestBody []byte) (string, error) {
	// Create custom HTTP client with a 10-minute timeout
	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	// Create a new request
	req, err := http.NewRequest("POST", "http://ollama.local:11111/api/generate", bytes.NewBuffer(requestBody))
	if err != nil {
		msg := fmt.Sprintf("Failed to create new request: %v", err)
		return msg, err
	}

	req.Header.Set("Content-Type", "application/json")

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		msg := fmt.Sprintf("Failed to make request to external service: %v", err)
		return msg, err
	}

	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		msg := fmt.Sprintf("Failed to read response from external service: %v", err)
		return msg, err
	}

	var generateResponse LLMGenerateAnswer

	err = json.Unmarshal(responseBody, &generateResponse)
	if err != nil {
		msg := fmt.Sprintf("Could not unmarshal response: %v", err)
		return msg, err
	}

	return generateResponse.Response, nil
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
	http.HandleFunc("/async/generateMemories", generateMemoriesHandler)
	http.HandleFunc("/async/login", loginHandler)
	log.Fatal(http.ListenAndServe("0.0.0.0:32225", nil))
}
