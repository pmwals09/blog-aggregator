package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
  respondWithJSON(w, http.StatusOK, newFeed)
}
