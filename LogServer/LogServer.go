/*
 * LogServer.go
 *
 * A simple log server that listens for incoming TCP connections on port 8080.
 * Clients can send log entries in protobuf format, which the server will unmarshal and print to the console and a logFile.
 * Features to add:
 *    - Input arguments for port, pollingRate and log file path.
 *    - Write logs to a file instead of just printing, something that LogManager can read from.
 *    - Add log rotation based on time.
 *	  - Display logs in a web interface
 *
 *
 * References:
 * - Go net package: 			https://golang.org/pkg/net/
 * - Protobuf documentation: 	https://developers.google.com/protocol-buffers
 */

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	// logserver/pb is the local package generated from our .proto file.
	"logserver/pb"

	"google.golang.org/protobuf/proto"
)

type clientInfo struct {
	//TODO We could store additional info about the client here, e.g. connection time, number of messages received, etc.
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
)

const (
	// DefaultPort is the standard port for gRPC and related Protobuf services.
	DefaultPort = ":50051"
)

func main() {
	fmt.Printf("🤖 Log Server is starting...\n")

	//TODO Take as parameter?
	var pollingRate = 2000

	today := time.Now().Format("2006-01-02")
	logFilename := fmt.Sprintf("DevLog_%s.txt", today)

	// Open the message file during startup, and keep it open, but protect the file handle with a mutex in handleConnection().
	//TODO Is this the best way to do this? We could also open and close the file for each message.
	receivedMessagesFile, err := os.OpenFile(logFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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

	fmt.Println("🤖 Log Server is ready and listening!")

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

// handleConnection processes logs from a single client connection.
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
	if client.previouslySeen {
		client.numberOfMessages += 1
	} else {
		client.previouslySeen = true
		client.numberOfMessages = 1
	}
	client.connectedAt = time.Now()
	seenClients[clientAddress] = client
	seenClientsMutex.Unlock()

	// Create a byte slice (dynamic array) as a buffer.
	messageBuffer := make([]byte, 1024)
	for {
		// Read() blocks until data arrives.
		bytesRead, err := clientConnection.Read(messageBuffer)
		if err != nil {
			// io.EOF is a standard error indicating the client closed the connection.
			if err != io.EOF {
				log.Printf("Read error: %v", err)
			}
			break
		}

		// Instantiate the generated Protobuf struct (pb.LogEntry).
		logEntry := &pb.LogEntry{}
		// Unmarshal the received bytes in the buffer into the struct logEntry.
		if err := proto.Unmarshal(messageBuffer[:bytesRead], logEntry); err != nil {
			log.Printf("Failed to unmarshal: %v", err)
			continue
		}

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
		msgFileMutex.Unlock()
	}
}

// shutdown prints the final report.
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
		for address, info := range seenClients {
			fmt.Printf(" - %s (connected at %s, messages received: %d)\n", address, info.connectedAt.Format(time.RFC3339), info.numberOfMessages)
		}
	}
	seenClientsMutex.Unlock()
}

// initializeListener handles the setup of the TCP socket.
// In C++, this would be a factory function returning a unique_ptr to a socket.
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
