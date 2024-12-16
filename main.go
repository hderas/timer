package main

import (
    "context"
    "encoding/gob"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "sync"
    "time"

    "github.com/gorilla/mux"
    "github.com/gorilla/sessions"
    "github.com/gorilla/websocket"
    "golang.org/x/crypto/bcrypt"
)

type TimeResponse struct {
    CurrentTime string `json:"currentTime"`
}

type Config struct {
    Timestamp     string `json:"timestamp"`
    Day           string `json:"day"`
    MatchDuration int    `json:"matchDuration"`
    PauseDuration int    `json:"pauseDuration"`
}

type LogEntry struct {
    Event         string    `json:"event"`
    Time          time.Time `json:"time"`
    Configuration *Config   `json:"configuration,omitempty"`
}

type ControlMessage struct {
    Command string
    Config  *Config
}

var (
    logs         []LogEntry = []LogEntry{} // Initialize as an empty slice
    logsMutex    sync.Mutex
    controlChan  chan ControlMessage
    timerRunning bool
    timerMutex   sync.Mutex

    store   *sessions.CookieStore
    users   map[string]string // username -> hashed password
    userMux sync.Mutex

    clients      = make(map[*websocket.Conn]bool) // Connected clients
    clientsMutex sync.Mutex
    broadcast    = make(chan WebSocketMessage)    // Broadcast channel

    upgrader = websocket.Upgrader{
        ReadBufferSize:  1024,
        WriteBufferSize: 1024,
        CheckOrigin: func(r *http.Request) bool {
            return true // Allow connections from any origin
        },
    }
)

// WebSocketMessage defines the structure of messages sent over WebSocket
type WebSocketMessage struct {
    Action string `json:"action"`
}

func init() {
    // Register types for session encoding
    gob.Register(&Config{})
    gob.Register(&LogEntry{})
}

func main() {
    // Initialize the session store
    store = sessions.NewCookieStore([]byte("your-secret-key"))
    store.Options = &sessions.Options{
        Path:     "/",
        MaxAge:   3600 * 1, // 1 hour
        HttpOnly: true,
        // Secure:   true, // Uncomment when using HTTPS
    }

    // Initialize users
    users = make(map[string]string)
    // Create the user 'trondm' with password 'password123'
    err := createUser("trondm", "password123")
    if err != nil {
        log.Fatal(err)
    }

    // Initialize control channel.
    controlChan = make(chan ControlMessage)

    // Initialize the router
    r := mux.NewRouter()

    // Protected routes
    r.HandleFunc("/", authMiddleware(indexHandler)).Methods("GET")
    r.HandleFunc("/time", authMiddleware(timeHandler)).Methods("GET")
    r.HandleFunc("/start", authMiddleware(startHandler)).Methods("POST")
    r.HandleFunc("/stop", authMiddleware(stopHandler)).Methods("POST")
    r.HandleFunc("/logs", authMiddleware(logsHandler)).Methods("GET")
    r.HandleFunc("/status", authMiddleware(statusHandler)).Methods("GET")
    r.HandleFunc("/clear_logs", authMiddleware(clearLogsHandler)).Methods("POST")
    r.HandleFunc("/ws", authMiddleware(wsHandler)).Methods("GET") // WebSocket endpoint

    // Public routes
    r.HandleFunc("/login", loginHandler)
    r.HandleFunc("/logout", logoutHandler)
    fs := http.FileServer(http.Dir("./static"))
    r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fs))

    // Start the server
    go func() {
        if err := http.ListenAndServe(":3443", r); err != nil {
            log.Fatal(err)
        }
    }()

    // Start the broadcaster
    go handleMessages()

    // Main loop to handle start/stop commands.
    for {
        msg := <-controlChan
        switch msg.Command {
        case "start":
            if msg.Config == nil {
                log.Println("No configuration provided with start command")
                continue
            }
            go startMainLoop(*msg.Config)
        case "stop":
            stopMainLoop()
        }
    }
}

// User Creation Function
func createUser(username, password string) error {
    userMux.Lock()
    defer userMux.Unlock()

    if _, exists := users[username]; exists {
        return fmt.Errorf("user already exists")
    }

    hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return err
    }

    users[username] = string(hashedPassword)
    return nil
}

// Authentication Middleware
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        session, _ := store.Get(r, "session")
        if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
            // User not authenticated
            http.Redirect(w, r, "/login", http.StatusFound)
            return
        }
        next.ServeHTTP(w, r)
    }
}

// Login Handler
func loginHandler(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        http.ServeFile(w, r, "./static/login.html")
    case http.MethodPost:
        username := r.FormValue("username")
        password := r.FormValue("password")

        userMux.Lock()
        hashedPassword, exists := users[username]
        userMux.Unlock()

        if !exists || bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password)) != nil {
            // Log failed login attempt on the server side
            log.Printf("Failed login attempt for username: %s\n", username)
            http.Error(w, "Invalid username or password", http.StatusUnauthorized)
            return
        }

        // Log successful login on the server side
        log.Printf("User %s logged in successfully\n", username)

        session, _ := store.Get(r, "session")
        session.Values["authenticated"] = true
        session.Values["username"] = username // Store username in session if needed
        session.Save(r, w)

        http.Redirect(w, r, "/", http.StatusFound)
    default:
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    }
}

// Logout Handler
func logoutHandler(w http.ResponseWriter, r *http.Request) {
    session, _ := store.Get(r, "session")
    username, _ := session.Values["username"].(string)

    // Log logout event on the server side
    log.Printf("User %s logged out\n", username)

    // Clear session
    session.Values["authenticated"] = false
    session.Values["username"] = ""
    session.Save(r, w)
    http.Redirect(w, r, "/login", http.StatusFound)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "./static/index.html")
}

func timeHandler(w http.ResponseWriter, r *http.Request) {
    currentTime := time.Now().Format("15:04:05")
    response := TimeResponse{CurrentTime: currentTime}

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}

func startHandler(w http.ResponseWriter, r *http.Request) {
    var cfg Config
    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "Unable to read request body", http.StatusBadRequest)
        return
    }
    err = json.Unmarshal(body, &cfg)
    if err != nil {
        http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
        return
    }

    // Validate and set default values if necessary
    if cfg.MatchDuration <= 0 {
        cfg.MatchDuration = 18 // default to 18 minutes
    }
    if cfg.PauseDuration <= 0 {
        cfg.PauseDuration = 2 // default to 2 minutes
    }

    timerMutex.Lock()
    defer timerMutex.Unlock()
    if timerRunning {
        http.Error(w, "Timer is already running", http.StatusConflict)
        return
    }
    timerRunning = true

    // Log to stdout
    log.Printf("Timer started with configuration: %+v\n", cfg)

    // Log the start event with configuration
    logsMutex.Lock()
    logs = append(logs, LogEntry{
        Event:         "Timer Started",
        Time:          time.Now(),
        Configuration: &cfg,
    })
    logsMutex.Unlock()

    controlChan <- ControlMessage{
        Command: "start",
        Config:  &cfg,
    }

    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Timer started")
}

func stopHandler(w http.ResponseWriter, r *http.Request) {
    timerMutex.Lock()
    defer timerMutex.Unlock()
    if !timerRunning {
        http.Error(w, "Timer is not running", http.StatusConflict)
        return
    }
    timerRunning = false

    // Log to stdout
    log.Println("Timer stopped")

    // Log the stop event
    logsMutex.Lock()
    logs = append(logs, LogEntry{
        Event: "Timer Stopped",
        Time:  time.Now(),
    })
    logsMutex.Unlock()

    controlChan <- ControlMessage{
        Command: "stop",
    }

    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Timer stopped")
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
    logsMutex.Lock()
    defer logsMutex.Unlock()
    if logs == nil {
        logs = []LogEntry{}
    }
    w.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w).Encode(logs); err != nil {
        http.Error(w, "Failed to encode logs", http.StatusInternalServerError)
        log.Println("Error encoding logs:", err)
    }
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
    timerMutex.Lock()
    running := timerRunning
    timerMutex.Unlock()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]bool{
        "timerRunning": running,
    })
}

func clearLogsHandler(w http.ResponseWriter, r *http.Request) {
    logsMutex.Lock()
    defer logsMutex.Unlock()

    logs = []LogEntry{} // Clear the logs slice

    w.WriteHeader(http.StatusOK)
}

var cancelFunc context.CancelFunc

func startMainLoop(cfg Config) {
    ctx, cancel := context.WithCancel(context.Background())
    cancelFunc = cancel

    // Parse the date and time
    layout := "2006-01-02 15:04:05"
    startTimeStr := fmt.Sprintf("%s %s", cfg.Day, cfg.Timestamp)
    startTime, err := time.ParseInLocation(layout, startTimeStr, time.Local)
    if err != nil {
        log.Println("Invalid date or time format")
        stopMainLoop()
        return
    }

    now := time.Now()
    if startTime.Before(now) {
        log.Println("Start time is in the past")
        stopMainLoop()
        return
    }

    // Log to stdout
    log.Printf("Main loop started, will begin at %s\n", startTime.Format("2006-01-02 15:04:05"))

    // Calculate the delay until the initial start time.
    initialDelay := startTime.Sub(now)

    select {
    case <-time.After(initialDelay):
        // Start the cycle of sending play commands for matches and breaks.
        for {
            select {
            case <-ctx.Done():
                return
            default:
                // Send "Match Start" command
                broadcastMessage("match_start")
                // Log the event
                logsMutex.Lock()
                logs = append(logs, LogEntry{
                    Event: "Match Start",
                    Time:  time.Now(),
                })
                logsMutex.Unlock()

                if !adjustDuration(ctx, time.Duration(cfg.MatchDuration)*time.Minute) {
                    return
                }

                // Send "Match End" command
                broadcastMessage("match_end")
                // Log the event
                logsMutex.Lock()
                logs = append(logs, LogEntry{
                    Event: "Match End",
                    Time:  time.Now(),
                })
                logsMutex.Unlock()

                if !adjustDuration(ctx, time.Duration(cfg.PauseDuration)*time.Minute) {
                    return
                }
            }
        }
    case <-ctx.Done():
        return
    }
}

func stopMainLoop() {
    if cancelFunc != nil {
        cancelFunc()
    }
}

// WebSocket handler
func wsHandler(w http.ResponseWriter, r *http.Request) {
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("WebSocket Upgrade Error:", err)
        return
    }

    // Register new client
    clientsMutex.Lock()
    clients[ws] = true
    clientsMutex.Unlock()

    for {
        // Read message (we don't expect any messages from the client)
        _, _, err := ws.ReadMessage()
        if err != nil {
            clientsMutex.Lock()
            delete(clients, ws)
            clientsMutex.Unlock()
            ws.Close()
            break
        }
    }
}

// Broadcast messages to clients
func broadcastMessage(action string) {
    msg := WebSocketMessage{Action: action}
    broadcast <- msg
}

func handleMessages() {
    for {
        msg := <-broadcast
        clientsMutex.Lock()
        for client := range clients {
            err := client.WriteJSON(msg)
            if err != nil {
                log.Printf("WebSocket Error: %v", err)
                client.Close()
                delete(clients, client)
            }
        }
        clientsMutex.Unlock()
    }
}

func adjustDuration(ctx context.Context, intendedDuration time.Duration) bool {
    // Sleep for the intended duration or until context is cancelled.
    select {
    case <-time.After(intendedDuration):
        return true
    case <-ctx.Done():
        return false
    }
}
