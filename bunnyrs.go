package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

type ServerFlags struct {
	enabled bool
	port    string
}

type ClientFlags struct {
	enabled bool
	target  string
}

// CLI Flags
var serverFlags ServerFlags
var clientFlags ClientFlags

// Color constants for printing
const (
	ansiPastelPink   = "\033[38;5;219m"
	ansiPastelBlue   = "\033[96m"
	ansiPastelGreen  = "\033[38;5;151m"
	ansiPastelOrange = "\033[38;5;215m"
	ansiPastelRed    = "\033[91m"
	ansiReset        = "\033[0m"
	ansiBold         = "\033[1m"
	ansiUnderline    = "\033[4m"
)

func amain() {
	flag.BoolVar(&serverFlags.enabled, "s", false, "enable server mode")
	flag.StringVar(&serverFlags.port, "p", "5000", "port for server mode")
	flag.BoolVar(&clientFlags.enabled, "c", false, "enable client mode")
	flag.StringVar(&clientFlags.target, "t", "0.0.0.0:5000", "target for client mode")
	flag.Parse()

	switch {
	case clientFlags.enabled:
		var bc BunnyClient
		if err := bc.run(); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	case serverFlags.enabled:
		var bs BunnyServer
		if err := bs.run(); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	case clientFlags.enabled && serverFlags.enabled:
		fmt.Println("Cannot enable server mode and client mode at the same time")
		os.Exit(2)
	}
}

// Colored print with new line
func colorPrintln(color string, message string) {
	fmt.Println(color + message + ansiReset)
}

// Colored print
func colorPrint(color string, message string) {
	fmt.Print(color + message + ansiReset)
}

// Reads all data from a connection. (100MB limit)
// Expects a 4 bytes data length header
//
// Returns the data as a string
func readAllData(connection net.Conn) (*string, error) {
	dataLengthHeader := make([]byte, 4)
	if _, err := io.ReadFull(connection, dataLengthHeader); err != nil {
		return nil, err
	}
	dataLength := binary.BigEndian.Uint32(dataLengthHeader)
	if dataLength > 100*1024*1024 { // 100 MB limit
		return nil, errors.New("Data length too large.")
	}
	buffer := make([]byte, int(dataLength))
	if _, err := io.ReadFull(connection, buffer); err != nil {
		return nil, err
	}
	responseString := string(buffer)
	return &responseString, nil
}

// Sends string data through a connection
// Adds a 4 byte data length header
func sendData(connection net.Conn, data string) error {
	dataBytes := []byte(data)
	dataLengthHeader := make([]byte, 4)
	binary.BigEndian.PutUint32(dataLengthHeader, uint32(len(dataBytes)))
	packet := append(dataLengthHeader, dataBytes...)
	if _, err := connection.Write(packet); err != nil {
		if err == io.EOF {
			connection.Close()
		}
		return err
	}
	return nil
}

// The client mode
type BunnyClient struct {
	socketConn net.Conn
}

// Initializes the TCP client
func (bc *BunnyClient) initTcpClient() error {
	socketConn, err := net.Dial(
		"tcp",
		clientFlags.target,
	)
	if err != nil {
		return err
	}
	bc.socketConn = socketConn
	return nil
}

// Runs the client mode
func (bc *BunnyClient) run() error {
	if err := bc.initTcpClient(); err != nil {
		return err
	}

	defer bc.socketConn.Close()

	for {
		data, err := readAllData(bc.socketConn)
		if err != nil {
			return err
		}
		fmt.Println(*data)

		colorPrint(ansiPastelPink, "> ")
		fmt.Scan(data)
		if err := sendData(bc.socketConn, *data); err != nil {
			if errors.Is(err, syscall.EPIPE) {
				colorPrintln(ansiPastelRed, "Couldn't reach server")
				break
			} else {
				return err
			}
		}
	}
	return nil
}

// The server mode
type BunnyServer struct {
	socketListener net.Listener
}

// Initializes the TCP server
func (bs *BunnyServer) initTcpServer() error {
	listener, err := net.Listen(
		"tcp",
		fmt.Sprintf("0.0.0.0:%s", serverFlags.port),
	)
	if err != nil {
		return err
	}
	bs.socketListener = listener
	return nil
}

// Runs the server mode
func (bs *BunnyServer) run() error {
	colorPrintln(ansiPastelPink, ". ݁₊ ⊹ . bunnyrs (\\_/) ⟡ ݁ . ⊹ ₊ ݁. ")

	if err := bs.initTcpServer(); err != nil {
		return err
	}

	defer bs.socketListener.Close()
	for {
		connection, err := bs.socketListener.Accept()
		if err != nil {
			return err
		}
		go bs.handleClient(connection)
	}
}

// Handles clients
func (bs *BunnyServer) handleClient(connection net.Conn) error {
	colorPrintln(ansiPastelGreen, fmt.Sprintf("%s connected", connection.RemoteAddr()))

	defer connection.Close()

	if err := sendData(connection, ". ݁₊ ⊹ . bunnyrs (\\_/) ⟡ ݁ . ⊹ ₊ ݁. "); err != nil {
		return err
	}

	for {
		data, err := readAllData(connection)
		if err != nil {
			if err == io.EOF {
				colorPrintln(ansiPastelRed, fmt.Sprintf("%s disconnected", connection.RemoteAddr()))
				connection.Close()
			}
			return err
		}

		var response string
		success := true
		switch *data {
		case "test":
			response = bs.test()
		default:
			response = "Command does not exist"
			success = false
		}

		if success {
			colorPrintln(ansiPastelBlue, fmt.Sprintf("%s sent %s (˶ᵔ ᵕ ᵔ˶) <3", connection.RemoteAddr(), *data))
		} else {
			colorPrintln(ansiPastelOrange, fmt.Sprintf("%s sent %s (´ ・×・｀)", connection.RemoteAddr(), *data))
		}

		if err := sendData(connection, response); err != nil {
			return err
		}
	}
}

func (bs *BunnyServer) test() string {
	return "tested"
}
