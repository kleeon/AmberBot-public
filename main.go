package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

const (
	BOT_NAME   = "AMBER_BOT"
	RATE_LIMIT = 15
)

type postData struct {
	ID          int    `json:"id,omitempty"`
	Body        string `json:"body,omitempty"`
	CreatorName string `json:"creator_name,omitempty"`
	Name        string `json:"name,omitempty"`
}

type commentData struct {
	ID          int    `json:"id,omitempty"`
	PostID      int    `json:"post_id,omitempty"`
	CreatorName string `json:"creator_name,omitempty"`
	Content     string `json:"content,omitempty"`
	ParentID    int    `json:"parent_id,omitempty"`
	Auth        string `json:"auth,omitempty"`
	CreatorID   int    `json:"creator_id,omitempty"`
}

type messageData struct {
	Comment  commentData   `json:"comment,omitempty"`
	Comments []commentData `json:"comments,omitempty"`
	JWT      string        `json:"jwt,omitempty"`
	Post     postData      `json:"post,omitempty"`
}

type messageIn struct {
	OP   string      `json:"op"`
	Data messageData `json:"data"`
}

type messageOut struct {
	OP   string      `json:"op"`
	Data commentData `json:"data"`
}

type bot struct {
	id        int
	jwt       string
	postTimes map[string]time.Time
	ws        *websocket.Conn
}

func (b *bot) post(op, userName string, postID, parentID int) {
	msg := messageOut{
		OP: op,
		Data: commentData{
			Auth:      b.jwt,
			Content:   "Amber.",
			CreatorID: b.id,
			PostID:    postID,
			ParentID:  parentID,
		},
	}

	log.Println("Replied Amber. to " + userName)

	b.postTimes[userName] = time.Now()

	b.ws.WriteJSON(&msg)
}

func (b *bot) withinRateLimit(userName string, seconds int64) bool {
	t, ok := b.postTimes[userName]

	return !ok || t.Unix() < time.Now().Unix()-seconds
}

func main() {
	var amberBot bot

	amberBot.postTimes = make(map[string]time.Time)

	amberRegex := regexp.MustCompile(`(?mi)^.*\b(a ?m ?b ?e ?r)\b.*$`)

	err := godotenv.Load()
	if err != nil {
		log.Println(err)
		return
	}

	pass := os.Getenv("AMBER_BOT_PASSWORD")

outer:
	for {
		ws, _, err := websocket.DefaultDialer.Dial("wss://www.hexbear.net/api/v1/ws", nil)
		amberBot.ws = ws

		if err != nil {
			log.Println("Connection error:", err)

			time.Sleep(time.Millisecond * 100)
			continue
		}

		log.Println("Connected")

		postBody, _ := json.Marshal(map[string]string{
			"username_or_email": BOT_NAME,
			"password":          pass,
		})

		resp, err := http.Post("https://www.hexbear.net/api/v1/user/login", "application/json", bytes.NewBuffer(postBody))
		if err != nil {
			log.Println("Login error:", err)

			time.Sleep(time.Millisecond * 100)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)

		var respJson struct {
			Jwt string `json:"jwt"`
		}

		err = json.Unmarshal(respBody, &respJson)

		jwtString := respJson.Jwt

		amberBot.jwt = jwtString

		claims := jwt.MapClaims{}
		jwt.ParseWithClaims(jwtString, claims, nil)

		id, ok := claims["id"].(float64)

		if !ok {
			log.Println("Failed to get id from jwt. Reconnecting.")
			ws.Close()
			continue
		}

		amberBot.id = int(id)

		http.Get("https://www.hexbear.net/api/v1/site?auth=" + jwtString)
		http.Get("https://www.hexbear.net/api/v1/post/list?page=1&limit=40&sort=Hot&type_=All&auth=" + jwtString)
		http.Get("https://www.hexbear.net/api/v1/post/featured?auth=" + jwtString)

		payload, _ := json.Marshal(struct {
			Data struct {
				Community_id int `json:"community_id"`
			} `json:"data"`
			Op string `json:"op"`
		}{
			struct {
				Community_id int `json:"community_id"`
			}{
				0,
			},
			"CommunityJoinRoom",
		})

		ws.WriteMessage(websocket.TextMessage, payload)

		payload, _ = json.Marshal(struct {
			Data struct {
				Auth string `json:"auth"`
			} `json:"data"`
			Op string `json:"op"`
		}{
			struct {
				Auth string `json:"auth"`
			}{
				jwtString,
			},
			"UserJoin",
		})

		ws.WriteMessage(websocket.TextMessage, payload)

		failedReads := 0
		for {
			var msg messageIn
			err := ws.ReadJSON(&msg)

			if err != nil {
				log.Println("WebSocket read error:", err)

				failedReads++

				time.Sleep(time.Millisecond * 100)

				if failedReads >= 3 {
					log.Println("WebSocket connection failure. Reconnecting...")
					continue outer
				} else {
					continue
				}
			}

			failedReads = 0

			switch msg.OP {
			// case "Login":
			// 	jwtString = msg.Data.JWT
			// 	claims := jwt.MapClaims{}
			// 	jwt.ParseWithClaims(jwtString, claims, nil)

			// 	amberBot.id = int(claims["id"].(float64))
			// 	amberBot.jwt = jwtString
			case "CreateComment":
				creator := msg.Data.Comment.CreatorName
				cont := msg.Data.Comment.Content
				postID := msg.Data.Comment.PostID
				parentID := msg.Data.Comment.ID

				if amberRegex.MatchString(cont) &&
					creator != BOT_NAME &&
					amberBot.withinRateLimit(creator, RATE_LIMIT) {
					amberBot.post("CreateComment", creator, postID, parentID)
				}
			case "CreatePost":
				title := msg.Data.Post.Name
				body := msg.Data.Post.Body
				postID := msg.Data.Post.ID
				creator := msg.Data.Post.CreatorName

				if (amberRegex.MatchString(body) || amberRegex.MatchString(title)) &&
					creator != BOT_NAME &&
					amberBot.withinRateLimit(creator, RATE_LIMIT) {
					amberBot.post("CreateComment", creator, postID, 0)
				}
			}
		}
	}
}
