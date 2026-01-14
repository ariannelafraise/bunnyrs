package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

var serverFlags ServerFlags = ServerFlags{
	serverMode: false,
}

var clientFlags ClientFlags = ClientFlags{
	clientMode: false,
}

var targetFlags TargetFlags = TargetFlags{
	ipFlag:   "",
	portFlag: "",
}

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

func sendData(connection net.Conn, data string) error {
	dataBytes := []byte(data)
	var dataLengthHeader = make([]byte, 4)
	binary.BigEndian.PutUint32(dataLengthHeader, uint32(len(dataBytes)))
	if _, err := connection.Write(dataLengthHeader); err != nil {
		if err == io.EOF {
			connection.Close()
		}
		return err
	}
	if _, err := connection.Write(dataBytes); err != nil {
		if err == io.EOF {
			connection.Close()
		}
		return err
	}
	return nil
}

type BunnyClient struct {
	args       []string
	socketConn net.Conn
}

func (bc *BunnyClient) initTcpServer() error {
	socketConn, err := net.Dial(
		"tcp",
		fmt.Sprintf(
			"%s:%s",
			targetFlags.ipFlag,
			targetFlags.portFlag,
		),
	)
	if err != nil {
		return err
	}
	bc.socketConn = socketConn
	return nil
}

func (bc *BunnyClient) run() error {
	if err := bc.initTcpServer(); err != nil {
		return err
	}

	for {
		data, err := readAllData(bc.socketConn)
		if err != nil {
			if err == io.EOF {
				bc.socketConn.Close()
				break
			}
			return err
		}
		fmt.Println(*data)

		fmt.Print("> ")
		fmt.Scan(data)
		if err := sendData(bc.socketConn, *data); err != nil {
			return err
		}
	}
	return nil
}

type ServerFlags struct {
	serverMode bool
}

type ClientFlags struct {
	clientMode bool
}

type TargetFlags struct {
	ipFlag   string
	portFlag string
}

func main() {
	// add flags
	// flags parse
}
