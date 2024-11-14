package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

var PingError error
var ReceiveError error
var Commands map[string]func(*Client, string)

type Message struct {
    Sender   int `json:"sender"`
    Receiver int `json:"receiver"`
    Message  []byte `json:"message"`
    End      bool   `json:"end"`
}

func NewMessage(sender, receiver int, message []byte, end bool) (Message, error) {
    return Message{
        Sender:   sender,
        Receiver: receiver,
        Message:  message,
        End:      end,
    }, nil
}

func NewMessageFromJSON(bytes []byte) (Message, error) {
    var msg Message
    err := json.Unmarshal(bytes, &msg)
    if err != nil { return Message{}, err }

    return msg, nil
}

func NewMessageToJSON(sender, receiver int, message []byte, end bool) ([]byte, error) {
    msg := Message{
        Sender:   sender,
        Receiver: receiver,
        Message:  message,
        End:      end,
    }

    msg_bytes, err := json.Marshal(msg)
    if err != nil { return []byte{}, err }

    return msg_bytes, nil
}

func Serialize(data []byte) (message Message, err error) {
    err = json.Unmarshal(data, &message)
    return
}
type Client struct {
    Id           int	`json:"Id"`
    SecretKey    string `json:"SecretKey"`
    FriendlyName string `json:"FriendlyName"`
    ComputerName string `json:"ComputerName"`
    UserName     string `json:"UserName"`
    LocalIP      string `json:"LocalIP"`
    PublicIP     string `json:"PublicIP"`
    OS           string `json:"OS"`
    Conn         *websocket.Conn
}

func NewClient(secretKey, computerName, userName, localIP, publicIP, OS string) Client {
    if OS == "windows" {
        userName = strings.Split(userName, "\\")[1]
    }

    client := Client{
        SecretKey:    secretKey,
        ComputerName: computerName,
        UserName:     userName,
        LocalIP:      localIP,
        PublicIP:     publicIP,
        OS:           OS,
    }
    return client
}

func (client *Client) Start(websocket_URL string) error {
    conn, _, err := websocket.Dial(context.Background(), websocket_URL, nil)
    if err != nil { return err }
    defer conn.CloseNow()

    client.Conn = conn

    bytes, err := json.Marshal(client)
    if err != nil { return err }

    err = client.Write(client.Id, -1, bytes, true)
    if err != nil { return err }

    msg, err := client.Read()
    if err != nil { return err }
    client.Id = msg.Receiver

    go client.Ping()
    client.Send()
    if PingError != nil {
        log.Print("Отключён от сервера")
    }

    conn.Close(websocket.StatusNormalClosure, "")
    return nil
}

func (client *Client) Ping() {
    var err error
    for {
        err = client.Conn.Ping(context.Background())
        if err != nil {
            PingError = err
            return
        }
    }
}

func (client *Client) Send() {
    for {
        fmt.Print(">>> ")
        reader := bufio.NewReader(os.Stdin)
        bytes, _, _ := reader.ReadLine()
        
        function, ok := Commands[strings.Split(string(bytes), " ")[0]]
        if !ok { log.Print("команда не найдена"); continue }
        
        function(client, string(bytes))
    }
}

func ListUsersFunction(client *Client, cmd string) {
    for {
        err := client.Write(client.Id, -1, []byte(cmd), true)
        if err != nil { log.Print(err, 14) }

        msg, err := client.Read()
        if err != nil { log.Print(err, 1) }

        var clients []Client
        err = json.Unmarshal(msg.Message, &clients)
        if err != nil { log.Print(string(msg.Message), 2) }

        tw := table.NewWriter()
        tw.AppendHeader(table.Row{"#", "FriendlyName", "ComputerName", "UserName", "LocalIP", "PublicIP", "OS"})
        for _, client_ := range clients {
            friendlyName := client_.FriendlyName
            if client.Id == client_.Id { friendlyName = "You" }
            tw.AppendRow(table.Row{
                client_.Id,
                friendlyName,
                client_.ComputerName,
                client_.UserName,
                client_.LocalIP, 
                client_.PublicIP,
                client_.OS,
            })
        }

        tw.SortBy([]table.SortBy{{Name: "#", Mode: table.Asc}})
        tw.Style().Format.Header = text.FormatTitle
        tw.Style().Options.SeparateColumns = false
        
        fmt.Println(tw.Render())
        if msg.End { break }
    }
}

func ExecuteFunction(client *Client, cmd string) {
    cmdparts := strings.Split(cmd, " ")
    if len(cmdparts) < 4 { log.Print("слишком короткая команда"); return }

    var receiverID int
    if n, _ := fmt.Sscan(cmdparts[1], &receiverID); n == 0 { log.Print("ID не число"); return }

    err := client.Write(client.Id, receiverID, []byte(cmd), true)
    if err != nil { log.Print(err); return }

    reader   := bufio.NewReader(os.Stdin)
    file, _  := os.OpenFile(cmdparts[2], os.O_CREATE|os.O_APPEND, 0777)
    writer   := bufio.NewWriter(file)
    oswriter := bufio.NewWriter(os.Stdout)

    var writeEnd bool

    err = client.Write(client.Id, receiverID, []byte{}, true)
    if err != nil { log.Print(err) }

    go func() {
        for {
            msg, err := client.Read()
            if err != nil { log.Print(err) }

            if string(msg.Message) == "interrupted" { break }
            if msg.End { break }

            writer.Write(msg.Message)
            writer.WriteByte('\n')
            writer.Flush()

        }

        os.Stdin.SetReadDeadline(time.Now())
        writeEnd = true
    }()

    oswriter.Write([]byte("Напишите stop, чтобы остановить\n> "))
    oswriter.Flush()

    data := make([]byte, 65536)
    n, _ := reader.Read(data)
    if string(data[:n]) == "stop" || string(data[:n]) == "interrupt" {
        err := client.Write(client.Id, receiverID, []byte("interrupt"), true)
        if err != nil { log.Print(err, 16) }
    }

    for !writeEnd { time.Sleep(time.Second) }
    oswriter.Write([]byte("\n> executed"))
    oswriter.Flush()
}

func (client *Client) Read() (Message, error) {
    _, bytes, err := client.Conn.Read(context.Background())
    if err != nil { log.Print(err, 3); return Message{}, err }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { log.Print(err, 14); return Message{}, err }

    return msg, nil
}

func(client *Client) Write(sender, receiver int, bytes []byte, end bool) error {
    msg_bytes, err := NewMessageToJSON(sender, receiver, bytes, end)
    if err != nil { log.Print(err, 4); return err }

    err = client.Conn.Write(context.Background(), websocket.MessageText, msg_bytes)
    if err != nil { log.Print(err, 10); return err }

    return nil
}

func getUserName() (string, error) {
    user, err := user.Current()
    if err != nil { return "", err }
    return user.Username, nil
}

func getComputerName() (string, error) {
    computerName, err := os.Hostname()
    if err != nil { return "", err }
    return computerName, nil
}

func getLocalIP() (string, error) {
    interfaces, err := net.Interfaces()
    if err != nil { return "", err }

    for _, iface := range interfaces {
        addrs, err := iface.Addrs()
        if err != nil { return "", err }

        for _, addr := range addrs {
            var ip net.IP
            switch v := addr.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            }

            if ipv4 := ip.To4(); ipv4 != nil && !ip.IsLoopback() { return ipv4.String(), nil }
        }
    }

    return "", fmt.Errorf("локальный IP адрес не найден")
}

func getPublicIP() (string, error) {
    resp, err := http.Get("https://api.ipify.org")
    if err != nil { return "", err }
    defer resp.Body.Close()

    ip, err := io.ReadAll(resp.Body)
    if err != nil { return "", err }

    return string(ip), nil
}

func main() {
    Commands = map[string]func(*Client, string){}
    Commands["#!help"]       = nil
    Commands["#!list_users"] = ListUsersFunction
    Commands["#!execute"]    = ExecuteFunction

    secretKey := "44d927f49f08355c424b01da13a4913a2a59b50c3f353864844621f881c4d1bc"

    computerName, err := getComputerName()
    if err != nil { log.Print("Warning: ", err) }

    userName, err := getUserName()
    if err != nil { log.Print("Warning: ", err) }

    publicIP, err := getPublicIP()
    if err != nil { log.Print("Warning: ", err) }

    localIP, err := getLocalIP()
    if err != nil { log.Print("Warning: ", err) }

    client := NewClient(secretKey, computerName, userName, localIP, publicIP, runtime.GOOS)
    client.Start("ws://127.0.0.1:8080/ws/")
}
