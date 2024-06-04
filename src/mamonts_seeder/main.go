package main

import (
	"context"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FollowersList struct {
	Users         []Mamont `json:"users"`
	NextCursorStr string   `json:"next_cursor_str"`
}

type Mamont struct {
	ID   int64  `json:"id"`
	Name string `json:"screen_name"`
}

type User struct {
	UserID     string `json:"user_id"`
	AuthToken  string `json:"auth_token"`
	Ct0        string `json:"ct0"`
	XCsrfToken string `json:"x-csrf-token"`
}

type Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Proxy struct {
	Addr     string `json:"addr"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

var (
	mu            sync.Mutex
	userIds       = make(map[int64]string)
	wg            sync.WaitGroup
	fileMu        sync.Mutex
	groups        = make([]Group, 0)
	users         = make([]User, 0)
	existingUsers int
)

func main() {
	loadExistingUsers("./mamonts/mamonts.yaml")
	yamlFile, err := ioutil.ReadFile("./groups/groups.yaml")
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	var tmpMap map[string]string
	err = yaml.Unmarshal(yamlFile, &tmpMap)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	for name, idStr := range tmpMap {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		groups = append(groups, Group{
			ID:   id,
			Name: name,
		})
	}
	fileName := "./cookies/users.json"
	file, err := os.Open(fileName)
	if err != nil {
		log.Fatalf("Failed to open file: %s", err)
	}
	defer file.Close()

	byteValue, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("Failed to read file: %s", err)
	}
	err = json.Unmarshal(byteValue, &users)
	if err != nil {
		log.Fatalf("Failed to parse JSON: %s", err)
	}

	workerPool := make(chan struct{}, 200)

	for _, user := range users {
		for _, group := range groups {
			wg.Add(1)
			go func(user User, group Group) {
				workerPool <- struct{}{}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				worker(ctx, cancel, group.ID, user.AuthToken, user.UserID, user.XCsrfToken, "")
				<-workerPool
			}(user, group)
		}
	}

	wg.Wait()
	fmt.Println("existing users:", existingUsers)
}

func loadExistingUsers(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Fatalf("error reading file %s: %v", filename, err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			log.Fatalf("malformed line in %s: %s", filename, line)
		}
		name := strings.TrimSpace(parts[0])
		idStr := strings.TrimSpace(parts[1])
		idStr = strings.Trim(idStr, "\"")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			log.Fatalf("error parsing id from line in %s: %v", filename, err)
		}
		userIds[id] = name
	}
}

func worker(ctx context.Context, cancel context.CancelFunc, groupId int64, authToken, userId, csrfToken, nextCursor string) {
	defer wg.Done()

	if nextCursor != "" {
		fmt.Println("next cursor:", nextCursor)
	}

	cookie := fmt.Sprintf(
		"guest_id=v1%%3A171691201569628673; night_mode=2; d_prefs=MToxLGNvbnNlbnRfdmVyc2lvbjoyLHRleHRfdmVyc2lvbjoxMDAw; guest_id_ads=v1%%3A171691201569628673; guest_id_marketing=v1%%3A171691201569628673; personalization_id=\"v1_mHk/PlBGvc/Xi6/f5sCuHA==\"; g_state={\"i_l\":0}; kdt=pREpbfKw4sj0tTYHQ0hiF2l8tS2IUc0CmfELCzEG; auth_token=%s; ct0=%s; att=1-pZIoUQxxbztMdVY33b70EviP6LyRfeXZiO3i8BgW; twid=u%%3D%s; twtr_pixel_opt_in=Y; lang=en; _twitter_sess=BAh7CSIKZmxhc2hJQzonQWN0aW9uQ29udHJvbGxlcjo6Rmxhc2g6OkZsYXNo\"%%250ASGFzaHsABjoKQHVzZWR7ADoPY3JlYXRlZF9hdGwrCJYsxMOPAToMY3NyZl9p\"%%250AZCIlZDhjMDcyMmFkM2FkY2I2ZTcwM2RlMWMwYmE3MTU1MWM6B2lkIiVjMGI5\"%%250ANDVjOTI4MzgyMmM1OTlmNDYxZjcxMTg0ZGY5MA\"%%253D\"%%253D--1954ee2b9f0a4de74ae4d60d89bceace82518431",
		authToken, csrfToken, userId,
	)

	// получение случайного прокси
	proxy, _ := getRandomProxy()

	proxyURL, err := url.Parse(fmt.Sprintf("http://%s:%s@%s", proxy.Login, proxy.Password, proxy.Addr))
	if err != nil {
		log.Fatal(err)
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
	}

	if nextCursor != "" {
		nextCursor = fmt.Sprintf("&cursor=%s", nextCursor)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.twitter.com/1.1/followers/list.json?user_id=%d&skip_status=true&include_user_entities=false&count=200%s", groupId, nextCursor), nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-Csrf-Token", csrfToken)

	select {
	case <-ctx.Done():
		fmt.Println("context deadline exceeded")
		return
	default:
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}

		var followersList FollowersList
		err = json.Unmarshal(body, &followersList)
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}

		fileMu.Lock()
		defer fileMu.Unlock()

		f, err := os.OpenFile("./mamonts/mamonts.yaml", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()

		for _, user := range followersList.Users {
			mu.Lock()
			if _, ok := userIds[user.ID]; !ok {
				userIds[user.ID] = user.Name
				mu.Unlock()
			} else {
				existingUsers++
				mu.Unlock()
				continue
			}
			userStr := fmt.Sprintf("%s: \"%d\"\n", user.Name, user.ID)
			_, err = f.WriteString(userStr)
			if err != nil {
				log.Fatal(err)
			}
		}
		fmt.Println("finish process followers, existing users:", existingUsers)
		if followersList.NextCursorStr != "0" {
			wg.Add(1)
			go worker(ctx, cancel, groupId, authToken, userId, csrfToken, followersList.NextCursorStr)
		} else {
			fmt.Println("cursor is zero")
		}
	}
}

func getRandomProxy() (Proxy, string) {
	proxyFiles, err := filepath.Glob("./proxies/*.json")
	if err != nil {
		log.Fatalf("error reading proxy files: %v", err)
	}

	randomFile := proxyFiles[rand.Intn(len(proxyFiles))]
	proxyData, err := os.ReadFile(randomFile)
	if err != nil {
		log.Fatalf("error reading proxy file: %v", err)
	}

	var proxy Proxy
	err = json.Unmarshal(proxyData, &proxy)
	if err != nil {
		log.Fatalf("error unmarshalling proxy data: %v", err)
	}

	return proxy, randomFile
}
