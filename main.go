package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
)

func main() {
  err := godotenv.Load()
  if err != nil {
    fmt.Println("Error loading environment file")
    os.Exit(1)
    return
  }
  port := os.Getenv("PORT")

  r := chi.NewRouter()
  r.Use(cors.Handler(cors.Options{}))
  v1 := chi.NewRouter()
  v1.Get("/readiness", func(w http.ResponseWriter, r *http.Request) {
    respondWithJSON(w, http.StatusOK, map[string]string{"status": "ok"})
  })
  v1.Get("/err", func(w http.ResponseWriter, r *http.Request) {
    respondWithError(w, http.StatusInternalServerError, "Internal Server Error")
  })
  r.Mount("/v1", v1)

  s := http.Server{
    Addr: fmt.Sprintf(":%s", port),
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
