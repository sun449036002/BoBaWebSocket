package main

import (
"encoding/json"
"fmt"
"net/http"

"github.com/gorilla/websocket"
"github.com/satori/go.uuid"
	"github.com/garyburd/redigo/redis"
	"github.com/json-iterator/go"
	"talkGo/models"
	"strings"
	"errors"
	"strconv"
)

const ROOM_PERSON_NUMS_CACHE  = "room_person_nums_cache_%s"

type ClientManager struct {
	clients    map[int][]*Client
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

type Client struct {
	id     string
	socket *websocket.Conn
	send   chan []byte
	roomId string
	roomIdNum int
	sessionKey string //用户Session KEY
}

type Message struct {
	Sender    string `json:"sender,omitempty"`
	RoomIdNum    int `json:"roomIdNum,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Content   string `json:"content,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	AvatarUrl   string `json:"avatarUrl,omitempty"`
	PersonNum   string `json:"personNum,omitempty"`
}

type Content struct {
	Val string
	Sk string
}

var manager = ClientManager{
	broadcast:  make(chan []byte),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	clients:    make(map[int][]*Client),
}

func (manager *ClientManager) start() {
	rc, err := redis.Dial("tcp", "127.0.0.1:6379")
	if err != nil {
		fmt.Println("redis connect faild.....")
	}

	for {
		select {
		case conn := <-manager.register:
			manager.clients[conn.roomIdNum] = append(manager.clients[conn.roomIdNum], conn)
			fmt.Println("register:room ID =", conn.roomId)
			fmt.Println("register:sessionKey =", conn.sessionKey)

			num := len(manager.clients[conn.roomIdNum])
			fmt.Println("register 在线人数:", num)

			user, err := GetUserBySessionKey(rc, conn.sessionKey)
			if err != nil {
				fmt.Println("socket register :未从缓存中取到用户信息", err.Error())
			}

			jsonMessage, _ := json.Marshal(&Message{Content: "我 来 也~~~~~~.", Nickname:user.Username, PersonNum:strconv.Itoa(int(num))})
			manager.send(jsonMessage, conn.roomIdNum, conn)
		case conn := <-manager.unregister:
			for index, _conn := range manager.clients[conn.roomIdNum] {
				if _conn == conn {
					close(conn.send)
					manager.clients[conn.roomIdNum] = append(manager.clients[conn.roomIdNum][:index], manager.clients[conn.roomIdNum][index+1:]...)
					fmt.Println("unregister:room ID =", conn.roomId)

					//在线人数
					num := len(manager.clients[conn.roomIdNum])
					fmt.Println("unregister 在线人数:", num)

					user, err := GetUserBySessionKey(rc, conn.sessionKey)
					if err != nil {
						fmt.Println(err.Error())
						continue
					}

					jsonMessage, _ := json.Marshal(&Message{Content: "静静的我的走了，不带走一点云彩.", Nickname:user.Username, PersonNum:strconv.Itoa(int(num))})
					manager.send(jsonMessage, conn.roomIdNum, conn)
				}
			}
		case message := <-manager.broadcast:
			m := &Message{}
			json.Unmarshal(message, m)
			println("----- message manager.broadcast ----", m.PersonNum, m.RoomIdNum, len(manager.clients[m.RoomIdNum]))
			for index, conn := range manager.clients[m.RoomIdNum] {
				println("---------- in range clients[m.RoomIdNum]------------------")
				select {
				case conn.send <- message:
				default:
					close(conn.send)
					manager.clients[conn.roomIdNum] = append(manager.clients[conn.roomIdNum][:index], manager.clients[conn.roomIdNum][index+1:]...)
				}
			}
		}
	}
}

func (manager *ClientManager) send(message []byte, roomIdNum int, ignore *Client) {
	fmt.Println("clients数量:" + strconv.Itoa(len(manager.clients[roomIdNum])))
	for _, conn := range manager.clients[roomIdNum] {
		fmt.Println(conn,string(message))
		fmt.Println("conn != ignore ====> ", conn != ignore)
		//if conn != ignore {
			conn.send <- message
		//}
	}
	fmt.Println()
	fmt.Println()
}

func (c *Client) read() {
	defer func() {
		manager.unregister <- c
		c.socket.Close()
	}()

	for {
		_, message, err := c.socket.ReadMessage()
		if err != nil {
			manager.unregister <- c
			c.socket.Close()
			break
		}

		//取得用户信息
		rc, err := redis.Dial("tcp", "127.0.0.1:6379")
		if err != nil {
			fmt.Println("redis connect faild.....")
			break
		}
		jsonStr := string(message)
		jsonContent :=  &Content{}
		err = jsoniter.UnmarshalFromString(jsonStr, &jsonContent)
		if err != nil {
			fmt.Println(err.Error())
			break
		}
		fmt.Println(jsonContent.Val, jsonContent.Sk)

		user, err := GetUserBySessionKey(rc, jsonContent.Sk)
		if err != nil {
			fmt.Println(err.Error())
			break
		}

		fmt.Println("nickname", user.Username)

		jsonMessage, _ := json.Marshal(&Message{Sender: c.id, RoomIdNum:c.roomIdNum, Content: jsonContent.Val, Nickname : user.Username})
		manager.broadcast <- jsonMessage
	}
}

func (c *Client) write() {
	defer func() {
		c.socket.Close()
	}()

	println("-----------func write-----------------")
	for {
		select {
		case message, ok := <-c.send:
			println(ok)
			if !ok {
				c.socket.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			println(message, c, c.sessionKey, c.roomId)
			c.socket.WriteMessage(websocket.TextMessage, message)
		}
	}
}

func main() {
	fmt.Println("Starting application...")
	go manager.start()
	http.HandleFunc("/wss", wsPage)
	http.ListenAndServe(":12345", nil)
}

func wsPage(res http.ResponseWriter, req *http.Request) {
	conn, error := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(res, req, nil)

	if error != nil {
		http.NotFound(res, req)
		fmt.Println(error)
		return
	}

	bts := make([]byte, 0)
	lens, _ := req.Body.Read(bts)
	fmt.Println("request" , lens, bts, req.Host, req.Method)

	paramsMap := make(map[string]string)
	paramStr := strings.SplitAfter(req.URL.String(), "?")[1]
	params := strings.Split(paramStr, "&")
	for i := 0; i < len(params); i++ {
		fmt.Println(params[i])
		kv := strings.Split(params[i], "=")
		paramsMap[kv[0]] = kv[1]
	}

	println(paramsMap["roomId"])
	println(paramsMap["sessionKey"])
	roomId, ok := paramsMap["roomId"]
	if !ok {
		roomId = ""
	}
	roomIdNum,_ := strconv.Atoi(strings.Split(roomId, "_")[1])
	sessionKey, ok := paramsMap["sessionKey"]
	if !ok {
		sessionKey = ""
	}

	uid, _:= uuid.NewV4()
	client := &Client{id: uid.String(), socket: conn, send: make(chan []byte), roomId:roomId, roomIdNum:roomIdNum, sessionKey:sessionKey}

	manager.register <- client

	go client.read()
	go client.write()
	//fmt.Println("server start ok")
}


/**
获取 缓存中的用户信息
 */
func GetUserBySessionKey(rc redis.Conn, sessionKey string) (models.User, error) {
	userinfoJson, err := redis.String(rc.Do("GET", "userinfo_" + sessionKey))
	if err != nil {
		print("get user info redis fail")
		return models.User{}, errors.New("get user info redis fail, sessionKey=" + sessionKey + "," + err.Error())
	}
	user := models.User{}
	err = jsoniter.UnmarshalFromString(userinfoJson, &user)
	if err != nil {
		fmt.Println("userinfo json decode faild")
		return models.User{}, errors.New("userinfo json decode faild, sessionKey=" + sessionKey + "," + err.Error())
	}

	return user, nil
}