package main

import (
	"context"
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

type apiConfig struct {
	DB *database.Queries
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

	v1.Get("/users", func(w http.ResponseWriter, r *http.Request) {
    handleUsersGet(w, r, ac)
  })
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
	newUser, err := ac.DB.CreateUser(context.Background(), user)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating user")
		fmt.Println(err)
		return
	}
	respondWithJSON(w, http.StatusCreated, newUser)
}

func handleUsersGet(w http.ResponseWriter, r *http.Request, ac apiConfig) {
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
  user, err := ac.DB.GetUserByApiKey(context.Background(), key)
  if err != nil {
    respondWithError(w, http.StatusUnauthorized, "Unauthorized")
    return
  }
  respondWithJSON(w, http.StatusOK, user)
}
