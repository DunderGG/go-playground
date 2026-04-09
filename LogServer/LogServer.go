// LogServer listens for incoming TCP connections from LogClients and 
// broadcasts telemetry data to a web-based dashboard via WebSockets.
//
// It unmarshals binary Protobuf data, persists it to disk, and uses 
// Goroutines to handle multiple concurrent clients and web connections.
//
// Features to add:
//  - Input arguments for port, pollingRate and log file path.
//  - Add log rotation based on time.
//  - Advanced log filtering and search in the web interface.
//
// Note: Uses github.com/gorilla/websocket for real-time dashboard updates.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"

	// logserver/pb is the local package generated from our .proto file.
	"logserver/pb"

	"google.golang.org/protobuf/proto"
)

type clientInfo struct {
	//TODO We could store additional info about the client here, e.g. connection time, number of messages received, etc.
	address        string
	previouslySeen bool
	// Not very useful since the logs themselves contain timestamps...
	connectedAt      time.Time
	numberOfMessages int
}

var (
	// seenClients maps client address (IP:Port) to true.
	//   Not sure if we need this for anything, so just store some info about the client.
	seenClients = make(map[string]clientInfo)
	// receivedMessagesFile is the file handle for the log file we write incoming protobuf messages to.
	receivedMessagesFile *os.File

	// Use a sync.Mutex because multiple goroutines (handleConnection) will write to the map and message file.
	seenClientsMutex sync.Mutex
	msgFileMutex     sync.Mutex

	// WebSocket upgrader
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Connected dashboard clients
	dashboardClients = make(map[*websocket.Conn]bool)
	dashboardMutex   sync.Mutex
)

const (
	// DefaultPort is the standard port for gRPC and related Protobuf services.
	DefaultPort = ":50051"
	// WebPort is the port for the dashboard web interface.
	WebPort = ":8080"
)

func main() {
	fmt.Printf("🤖 Log Server is starting...\n")

	//TODO Take as parameter?
	var pollingRate = 2000

	today := time.Now().Format("2006-01-02")
	logFilename := fmt.Sprintf("RcvdMsgs_%s.txt", today)

	// Open the message file during startup, and keep it open, but protect the file handle with a mutex in handleConnection().
	//TODO Is this the best way to do this? We could also open and close the file for each message.
	var err error
	receivedMessagesFile, err = os.OpenFile(logFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening file %s: %v", logFilename, err)
		return
	}
	defer receivedMessagesFile.Close()

	// Create a context that is cancelled when we receive an interrupt signal
	// We want to be able to print stuff when we are done, rather than just aborting the application immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the listener using our new helper function.
	tcpListener := initializeListener()
	// 'defer' ensures tcpListener.Close() is called when the function (main) returns.
	defer tcpListener.Close()

	// Start the WebSocket and Web Server in a separate goroutine.
	// In C++, this is equivalent to std::thread(startWebServer).detach().
	// Go handles the stack management and context switching efficiently.
	go startWebServer()

	fmt.Println("🤖 Log Server is ready and listening!")
	fmt.Printf("🌐 Dashboard is (hopefully) available at http://localhost%s\n", WebPort)

	for {
		// Check if the context has been canceled (e.g., via Ctrl+C).
		select {
		case <-ctx.Done():
			shutdown(0)
			return
		default:
			// cast to *net.TCPListener to use SetDeadline.
			if tcpl, ok := tcpListener.(*net.TCPListener); ok {
				tcpl.SetDeadline(time.Now().Add(time.Duration(pollingRate) * time.Millisecond))
			}

			// Accept() will now unblock after 1 second if no connection arrives.
			clientConnection, err := tcpListener.Accept()
			if err != nil {
				// Check if the error is a timeout (Expected when no one connects).
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					fmt.Printf("⏱️  No connection received in the last %d ms, trying again...\n", pollingRate)
					continue
				}
				shutdown(2)
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
			// 'go' keyword starts a "goroutine" (a lightweight thread).
			// This handles each connection concurrently without manual thread management.
			go handleConnection(clientConnection)
		}
	}
}

// handleConnection processes logs from a single clientConnection (net.Conn).
//
// Logic:
//  1. Update persistent client info in the seenClients map.
//  2. Enter a loop to read length-prefixed protocol buffer messages.
//  3. First read 4 bytes for the message size.
//  4. Read exactly 'size' bytes for the payload.
//  5. Unmarshal to pb.LogEntry, broadcast to dashboards, and append to file.
func handleConnection(clientConnection net.Conn) {
	// RAII-style cleanup for the connection.
	defer clientConnection.Close()

	// Get the client's address (string format: "IP:Port").
	clientAddress := clientConnection.RemoteAddr().String()
	fmt.Printf("👋 Client connected: %s\n", clientAddress)

	// Thread-safe update of the seenClients map.
	// If already locked, this will block until the mutex is available, ensuring only one goroutine updates the map at a time.
	seenClientsMutex.Lock()
	// Check if we've seen this client before, and update the info accordingly.
	client := seenClients[clientAddress]
	client.address = clientAddress
	if client.previouslySeen {
		client.numberOfMessages += 1
	} else {
		client.previouslySeen = true
		client.numberOfMessages = 1
	}
	client.connectedAt = time.Now()
	seenClients[clientAddress] = client
	seenClientsMutex.Unlock()

	for {
		// Read 4 bytes for the size
		sizeBuf := make([]byte, 4)
		if _, err := io.ReadFull(clientConnection, sizeBuf); err != nil {
			if err != io.EOF {
				log.Printf("Read size error: %v", err)
			}
			break
		}
		size := binary.BigEndian.Uint32(sizeBuf)

		// Read the actual protobuf data
		messageBuffer := make([]byte, size)
		if _, err := io.ReadFull(clientConnection, messageBuffer); err != nil {
			log.Printf("Read message error: %v", err)
			break
		}

		// Instantiate the generated Protobuf struct (pb.LogEntry).
		logEntry := &pb.LogEntry{}
		// Unmarshal the received bytes into the struct logEntry.
		if err := proto.Unmarshal(messageBuffer, logEntry); err != nil {
			log.Printf("Failed to unmarshal: %v", err)
			continue
		}

		fmt.Printf("📢 Broadcasting log entry: %s\n", logEntry.Message)
		// Broadcast to all connected dashboard clients
		broadcastToDashboards(logEntry)

		// Save the message to the file.
		message := fmt.Sprintf("[%s] [%d]: %s\n",
			logEntry.Level,
			logEntry.Timestamp,
			logEntry.Message)

		// Protect the file from concurrent writes.
		msgFileMutex.Lock()
		if _, err := receivedMessagesFile.WriteString(message); err != nil {
			log.Printf("Error writing to file: %v", err)
		}
		receivedMessagesFile.Sync() // Force flush to disk
		msgFileMutex.Unlock()
	}
}

// shutdown prints the final server report and performs a graceful exit using the
// provided exit status code.
//
// Logic:
//  1. Swaps between exit codes to print a relevant status message.
//  2. Safely iterates through the 'seenClients' map to display session stats.
func shutdown(code int) {
	switch code {
	case 0:
		fmt.Println("✅ LogServer is shutting down gracefully.")
	case 1:
		//fmt.Println("1️⃣ Shutdown due to an error with net.Listen.")
	case 2:
		fmt.Println("2️⃣ Shutdown due to an error with net.Accept.")
	default:
		fmt.Printf("🛑 Shutdown with code %d.\n", code)
	}

	// Protect access to the map during shutdown.
	seenClientsMutex.Lock()
	if len(seenClients) == 0 {
		fmt.Println("No clients connected during this session.")
	} else {
		fmt.Printf("Total unique clients seen: %d\n", len(seenClients))
		for _, info := range seenClients {
			fmt.Printf(" - %s\n\tconnected at %s\n\tmessages received: %d\n", info.address, info.connectedAt.Format(time.RFC3339), info.numberOfMessages)
		}
	}
	seenClientsMutex.Unlock()
}

// initializeListener configures and starts the TCP listener, returning a net.Listener ready for Accept().
//
// Logic:
//  1. Attempt to bind to 'DefaultPort' (50051).
//  2. If binding fails (e.g. port taken), attempts to bind to a random available port.
func initializeListener() net.Listener {
	// Listen creates a TCP listener. Similar to socket(), bind(), and listen() in C++.
	// The empty host syntax (:port) means Listen listens on all addresses.
	tcpListener, err := net.Listen("tcp", DefaultPort)

	//TODO Maybe check for different types of errors, e.g. EADDRINUSE
	if err != nil {
		fmt.Printf("🚨 Error: Port %s might be in use. Trying a random available port...\n", DefaultPort)
		//TODO This is probably very dumb... How is a client supposed to connect to a random port?
		//       We might want to implement a retry mechanism with a few different ports, or exit with an error message.
		tcpListener, err = net.Listen("tcp", ":0") // Listen on any available port
		if err != nil {
			// If we can't even get a random port, we crash (std::terminate).
			log.Fatalf("Failed to listen on any port: %v", err)
		}
	}

	fmt.Printf("✅ Log Server is listening on %s\n", tcpListener.Addr().String())
	return tcpListener
}

// startWebServer runs the HTTP server for the dashboard and WebSockets.
//
// Logic:
//  1. Registers handlers for the root path and /ws path.
//  2. Root handler performs path-hunting for 'index.html'.
//  3. Starts the blocking ListenAndServe loop.
func startWebServer() {
	// Register a route for the WebSocket connection.
	http.HandleFunc("/ws", handleWebSocket)

	// Register a route for the root path (the dashboard UI).
	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		// Try multiple paths to find the index.html depending on how the server was started
		// (e.g. from the root folder or from inside the LogServer folder).
		possiblePaths := []string{
			filepath.Join("LogServer", "index.html"),
			"index.html",
		}

		for _, path := range possiblePaths {
			// os.Stat is like checking for file existence with std::filesystem::exists.
			if _, err := os.Stat(path); err == nil {
				http.ServeFile(writer, request, path)
				return
			}
		}

		log.Printf("Could not find index.html. Paths tried: %v", possiblePaths)
		http.NotFound(writer, request)
	})

	// ListenAndServe blocks. Since we called this with 'go startWebServer()',
	// only this goroutine is blocked, not the whole application.
	if err := http.ListenAndServe(WebPort, nil); err != nil {
		log.Fatalf("Web server failed: %v", err)
	}
}

// handleWebSocket upgrades persistent HTTP connections to the WebSocket protocol, using the provided writer and request.
//
// Logic:
//  1. Performs the WebSocket handshake via the 'upgrader'.
//  2. Safely adds the connection to 'dashboardClients' map.
//  3. Enters a read-loop to detect client disconnects (EOF).
//  4. Uses 'defer' to ensure thread-safe removal from map on exit.
func handleWebSocket(writer http.ResponseWriter, request *http.Request) {
	// Upgrade the connection. Returns nil if the upgrade fails.
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	fmt.Printf("🔌 Dashboard connected: %s\n", conn.RemoteAddr())

	// Add the new client to our broadcast map.
	dashboardMutex.Lock()
	dashboardClients[conn] = true
	dashboardMutex.Unlock()

	// Use 'defer' to ensure the client is removed and the connection is closed
	// regardless of how this function exits (RAII-like behaviour).
	defer func() {
		fmt.Printf("🔌 Dashboard disconnected: %s\n", conn.RemoteAddr())
		dashboardMutex.Lock()
		delete(dashboardClients, conn)
		dashboardMutex.Unlock()
		conn.Close()
	}()

	// We must keep the goroutine alive to keep the connection open.
	// We read in a loop even though we don't expect messages from the client.
	// If ReadMessage returns an error, it means the client disconnected.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// broadcastToDashboards serializes the provided log entry to JSON and pushes to all active web clients.
//
// Logic:
//  1. Converts the binary Protobuf entry to JSON format.
//  2. Locks the dashboard client list.
//  3. Iterates and calls WriteMessage on each active connection.
//  4. Prunes stale/disconnected clients from the map.
func broadcastToDashboards(entry *pb.LogEntry) {
	// Marshal the struct into a JSON string.
	// Browser JavaScript can handle JSON natively, but not Protobuf.
	jsonBytes, err := protojson.Marshal(entry)
	if err != nil {
		log.Printf("JSON marshal error: %v", err)
		return
	}

	// Lock the client map to prevent concurrent modification which causes a panic in Go.
	dashboardMutex.Lock()
	defer dashboardMutex.Unlock()

	if len(dashboardClients) == 0 {
		fmt.Println("⚠️  No dashboard clients connected to receive log.")
	}

	// Iterate through all connected browsers and send the message.
	for client := range dashboardClients {
		err := client.WriteMessage(websocket.TextMessage, jsonBytes)
		if err != nil {
			// If sending fails, the client probably disconnected.
			log.Printf("WS write error: %v", err)
			client.Close()
			delete(dashboardClients, client)
		} else {
			fmt.Printf("✅ Pushed log to dashboard client: %s\n", client.RemoteAddr())
		}
	}
}
