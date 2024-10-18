package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Request struct {
    Method  string `json:"method"`
    Message string `json:"message"`
}

type Response struct {
    Method  string `json:"method"`
    Message string `json:"message"`
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
}

type Server struct {
    clients map[string]Client
    admins  map[string]string
}

func (server *Server) Connect(w http.ResponseWriter, r *http.Request){
    c, err := websocket.Accept(w, r, nil)
    if err != nil { log.Print(err) }
    defer c.CloseNow()

    ctx, cancel := context.WithTimeout(r.Context(), time.Second*10)
    defer cancel()

    var v []byte
    err = wsjson.Read(ctx, c, &v)
    if err != nil { log.Print(err) }
    ctx = c.CloseRead(ctx)
    
    var request Request
    err = json.Unmarshal(v, &request)
    if err != nil { log.Print(err) }

    log.Print(request)

    ctx = c.CloseRead(ctx)
    for { 
        log.Print(c.Ping(ctx))
    }

    c.Close(websocket.StatusNormalClosure, "")
}

func (server *Server) Start() {
    http.HandleFunc("/ws/",  server.Connect)
    // http.HandleFunc("/web/", nil)

    http.ListenAndServe("127.0.0.1:8080", nil)
}

func main() {
    server := Server{}
    server.clients = map[string]Client{}
    server.admins  = map[string]string{}

    if len(os.Args) < 3 {
        log.Fatalf("USAGE ERROR\nUSAGE:\n./server --file      <file_name>\n         --secretkey <secret_key>")
    }

    server.Start()
}