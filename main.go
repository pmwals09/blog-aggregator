package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/pmwals09/rss-aggregator/internal/database"
)

type authedHandler func(http.ResponseWriter, *http.Request, database.User)
type apiConfig struct {
	DB *database.Queries
}
type feedData struct {
	XMLName xml.Name `xml:"rss"`
	Text    string   `xml:",chardata"`
	Version string   `xml:"version,attr"`
	Atom    string   `xml:"atom,attr"`
	Channel struct {
		Text  string `xml:",chardata"`
		Title string `xml:"title"`
		Link  struct {
			Text string `xml:",chardata"`
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
		Description   string `xml:"description"`
		Generator     string `xml:"generator"`
		Language      string `xml:"language"`
		LastBuildDate string `xml:"lastBuildDate"`
		Item          []struct {
			Text        string `xml:",chardata"`
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Guid        string `xml:"guid"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
	FeedID uuid.UUID `xml:"feed_id"`
}

func (ac *apiConfig) middlewareAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if authorization == "" {
			respondWithError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		fields := strings.Fields(authorization)
		name, key := fields[0], fields[1]
		if name != "ApiKey" {
			respondWithError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		user, err := ac.DB.GetUserByApiKey(r.Context(), key)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		next(w, r, user)
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading environment file")
		os.Exit(1)
		return
	}
	port := os.Getenv("PORT")
	dbURL := os.Getenv("DB_URL")

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("Error connecting to database")
		os.Exit(2)
		return
	}

	dbQueries := database.New(db)

	ac := apiConfig{dbQueries}

	go getFeedsWorker(ac)

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{}))
	v1 := chi.NewRouter()
	v1.Get("/readiness", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	v1.Get("/err", func(w http.ResponseWriter, r *http.Request) {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error")
	})
	v1.Post("/users", func(w http.ResponseWriter, r *http.Request) {
		handleUsersPost(w, r, ac)
	})
	v1.Get("/users", ac.middlewareAuth(handleUsersGet))
	v1.Post("/feeds", ac.middlewareAuth(func(w http.ResponseWriter, r *http.Request, u database.User) {
		handleFeedsPost(w, r, u, ac)
	}))
	v1.Get("/feeds", func(w http.ResponseWriter, r *http.Request) {
		handleFeedsGet(w, r, ac)
	})
	v1.Post("/feed_follows", ac.middlewareAuth(func(w http.ResponseWriter, r *http.Request, u database.User) {
		handleFollowsPost(w, r, u, ac)
	}))
	v1.Delete("/feed_follows/{feedFollowID}", ac.middlewareAuth(func(w http.ResponseWriter, r *http.Request, u database.User) {
		handleFollowsDelete(w, r, u, ac)
	}))
	v1.Get("/feed_follows", ac.middlewareAuth(func(w http.ResponseWriter, r *http.Request, u database.User) {
		handleFollowsGet(w, r, u, ac)
	}))
	v1.Get("/posts", ac.middlewareAuth(func(w http.ResponseWriter, r *http.Request, u database.User) {
		handlePostsGet(w, r, u, ac)
	}))
	r.Mount("/v1", v1)

	s := http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: r,
	}
	log.Fatal(s.ListenAndServe())
}

func respondWithJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	data, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error writing JSON"))
	}
	w.WriteHeader(status)
	w.Write(data)
}

func respondWithError(w http.ResponseWriter, code int, msg string) {
	respondWithJSON(w, code, map[string]string{"error": msg})
}

func handleUsersPost(w http.ResponseWriter, r *http.Request, ac apiConfig) {
	type usersRequest struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(r.Body)
	defer r.Body.Close()
	newUsersReq := usersRequest{}
	err := decoder.Decode(&newUsersReq)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not decode json request")
		fmt.Println(err)
		return
	}
	user := database.CreateUserParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Name:      newUsersReq.Name,
	}
	newUser, err := ac.DB.CreateUser(r.Context(), user)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating user")
		fmt.Println(err)
		return
	}
	respondWithJSON(w, http.StatusCreated, newUser)
}

func handleUsersGet(w http.ResponseWriter, r *http.Request, u database.User) {
	respondWithJSON(w, http.StatusOK, u)
	return
}

func handleFeedsPost(w http.ResponseWriter, r *http.Request, u database.User, ac apiConfig) {
	type feedsPostRequest struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	decoder := json.NewDecoder(r.Body)
	defer r.Body.Close()
	newFeedsPostRequest := feedsPostRequest{}
	err := decoder.Decode(&newFeedsPostRequest)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to decode json")
		return
	}
	newFeed, err := ac.DB.CreateFeed(
		r.Context(),
		database.CreateFeedParams{
			ID:        uuid.New(),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Name:      newFeedsPostRequest.Name,
			Url:       newFeedsPostRequest.URL,
			UserID:    u.ID,
		})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save feed")
		return
	}
	newFeedFollow, err := ac.DB.CreateFeedFollow(
		r.Context(),
		database.CreateFeedFollowParams{
			ID:        uuid.New(),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			UserID:    u.ID,
			FeedID:    newFeed.ID,
		})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save feed follow")
		return
	}
	type createFeedResponse struct {
		Feed       database.Feed       `json:"feed"`
		FeedFollow database.FeedFollow `json:"feed_follow"`
	}
	respondWithJSON(w, http.StatusOK, createFeedResponse{
		Feed:       newFeed,
		FeedFollow: newFeedFollow,
	})
}

func handleFeedsGet(w http.ResponseWriter, r *http.Request, ac apiConfig) {
	feeds, err := ac.DB.ListFeeds(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to retrieve feeds")
		return
	}
	respondWithJSON(w, http.StatusOK, feeds)
}

func handleFollowsPost(w http.ResponseWriter, r *http.Request, u database.User, ac apiConfig) {
	type followsPostRequest struct {
		FeedId string `json:"feed_id"`
	}
	decoder := json.NewDecoder(r.Body)
	defer r.Body.Close()
	req := followsPostRequest{}
	err := decoder.Decode(&req)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to decode json")
		return
	}

	if req.FeedId == "" {
		respondWithError(w, http.StatusBadRequest, "Invalid feed ID")
		return
	}

	newFeedId, err := uuid.Parse(req.FeedId)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid feed ID")
		return
	}
	params := database.CreateFeedFollowParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		UserID:    u.ID,
		FeedID:    newFeedId,
	}
	follow, err := ac.DB.CreateFeedFollow(r.Context(), params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
	}
	respondWithJSON(w, http.StatusOK, follow)
	return
}

func handleFollowsDelete(w http.ResponseWriter, r *http.Request, u database.User, ac apiConfig) {
	feedFollowID := chi.URLParam(r, "feedFollowID")
	if feedFollowID == "" {
		respondWithError(w, http.StatusBadRequest, "Bad path")
		return
	}

	feedFollowUUID, err := uuid.Parse(feedFollowID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Bad path")
		return
	}
	err = ac.DB.DeleteFeedFollow(r.Context(), feedFollowUUID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Problem deleting feed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	return
}

func handleFollowsGet(w http.ResponseWriter, r *http.Request, u database.User, ac apiConfig) {
	feedFollows, err := ac.DB.GetUserFeedFollows(r.Context(), u.ID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to retrieve follows")
		return
	}
	respondWithJSON(w, http.StatusOK, feedFollows)
	return
}

func handlePostsGet(w http.ResponseWriter, r *http.Request, u database.User, ac apiConfig) {
	getPostArgs := database.GetPostsByUserParams{
		UserID: u.ID,
		Limit:  10,
	}
	posts, err := ac.DB.GetPostsByUser(r.Context(), getPostArgs)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "There was a problem getting the user's posts")
		return
	}
	type response struct {
		ID          uuid.UUID
		CreatedAt   time.Time
		UpdatedAt   time.Time
		Title       string
		Url         string
		Description *string
		PublishedAt *time.Time
		FeedID      uuid.UUID
		UserID      uuid.UUID
	}
	responses := make([]response, 0, len(posts))
	for _, post := range posts {
		r := response{
			ID:        post.ID,
			CreatedAt: post.CreatedAt,
			UpdatedAt: post.UpdatedAt,
			Title:     post.Title,
			Url:       post.Url,
			FeedID:    post.FeedID,
			UserID:    u.ID,
		}

		if !post.Description.Valid {
			r.Description = nil
		} else {
			r.Description = &post.Description.String
		}
		if !post.PublishedAt.Valid {
			r.PublishedAt = nil
		} else {
			r.PublishedAt = &post.PublishedAt.Time
		}

		responses = append(responses, r)
	}
	respondWithJSON(w, http.StatusOK, responses)
	return
}

func getFeed(url string) (feedData, error) {
	fd := feedData{}
	res, err := http.Get(url)
	if err != nil {
		fmt.Println(err)
		return fd, err
	}
	body, err := io.ReadAll(res.Body)
	defer res.Body.Close()
	err = xml.Unmarshal(body, &fd)
	if err != nil {
		return fd, err
	}
	fmt.Println(fd.Channel.Title)
	return fd, nil
}

func getFeedsWorker(ac apiConfig) {
	fmt.Println("Starting feeds worker...")
	errorChan := make(chan error)
	feedChan := make(chan feedData)
	done := make(chan struct{})
	for range time.Tick(time.Minute) {
		feeds, err := ac.DB.GetNextFeedsToFetch(context.Background(), 10)
		if err != nil {
			fmt.Println("Could not get next feeds: ", err)
			break
		}
		fmt.Println("Processing latest batch of feeds...")
		wg := sync.WaitGroup{}
		for _, feed := range feeds {
			wg.Add(1)
			fmt.Printf("Processing %s feed\n", feed.Name)
			go func(f database.Feed) {
				defer wg.Done()
				feedData, err := getFeed(f.Url)
				feedData.FeedID = feed.ID
				if err != nil {
					errorChan <- err
				}
				feedChan <- feedData
			}(feed)
		}
		go func() {
			wg.Wait()
			done <- struct{}{}
		}()

		select {
		case err := <-errorChan:
			fmt.Println(err)
		case feed := <-feedChan:
			for _, item := range feed.Channel.Item {
				fmt.Printf("Adding %s to posts...\n", item.Title)
				createParams := database.CreatePostParams{
					ID:        uuid.New(),
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
					Title:     item.Title,
					Url:       item.Link,
					FeedID:    feed.FeedID,
				}
				if item.Description != "" {
					createParams.Description = sql.NullString{String: item.Description, Valid: true}
				}
				createParams.Description = sql.NullString{String: "", Valid: false}

				if item.PubDate == "" {
					createParams.PublishedAt = sql.NullTime{Time: time.Now(), Valid: false}
				}
				pubTime, err := time.Parse(time.RFC1123Z, item.PubDate)
				if err != nil {
					createParams.PublishedAt = sql.NullTime{Time: time.Now(), Valid: false}
				}
				createParams.PublishedAt = sql.NullTime{Time: pubTime, Valid: true}

				ac.DB.CreatePost(context.Background(), createParams)
			}
		case <-done:
			break
		}
	}
}
