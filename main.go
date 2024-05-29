package main

import (
  "database/sql"
  "encoding/json"
  "fmt"
  "log"
  "net/http"
  "strconv"

  "github.com/gorilla/mux"
  _ "github.com/lib/pq"
  "github.com/nats-io/stan.go"
)

type Order struct {
	ID   int    `json:"id"`
	Data string `json:"data"`
}

type Service struct {
	db    *sql.DB
	sc    stan.Conn
	cache map[int]Order
}

func NewService(db *sql.DB, sc stan.Conn) *Service {
	return &Service{
		db:    db,
		sc:    sc,
		cache: make(map[int]Order),
  }
}

func (s *Service) Start() {
	_, err := s.sc.Subscribe("orders", func(msg *stan.Msg) {
		var order Order
		if err := json.Unmarshal(msg.Data, &order); err != nil {
		log.Println(err)
		return
		}
		s.cache[order.ID] = order
		s.saveToDB(order)
	})
	if err != nil {
		log.Fatal(err)
	}
}

func (s *Service) saveToDB(order Order) {
	_, err := s.db.Exec("INSERT INTO orders (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data", order.ID, order.Data)
	if err != nil {
		log.Println(err)
	}
}

func (s *Service) restoreCache() {
    rows, err := s.db.Query("SELECT id, data FROM orders")
    if err != nil {
        log.Println("Не удалось получить заказы из базы данных:", err)
        return
    }
    defer rows.Close()
    for rows.Next() {
        var order Order
        err := rows.Scan(&order.ID, &order.Data)
        if err != nil {
            log.Println("Ошибка при сканировании строки из базы данных:", err)
            continue
        }
        s.cache[order.ID] = order
        log.Printf("Заказ загружен из базы данных: %+v\n", order)
    }
    log.Println("Кэш восстановлен из базы данных")
}

func (s *Service) getOrder(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    id, err := strconv.Atoi(vars["id"])
    if err != nil {
        log.Println("Invalid order ID:", err)
        http.Error(w, "Invalid order ID", http.StatusBadRequest)
        return
    }

    order, found := s.cache[id]
    if !found {
        log.Println("Order not found for ID:", id)
        http.Error(w, "Order not found", http.StatusNotFound)
        return
    }

    log.Printf("Retrieved order from cache: %+v\n", order)
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(order)
}

func main() {
	connStr := "user=postgres dbname=mydb password=123 sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
    	log.Fatal(err)
  	}	
	defer db.Close()

  	sc, err := stan.Connect("test-cluster", "my-client", stan.NatsURL("nats://localhost:4223"))
  	if err != nil {
		log.Fatal(err)
	}
	defer sc.Close()

	s := NewService(db, sc)
	s.restoreCache()
	s.Start()

	r := mux.NewRouter()
	r.HandleFunc("/orders/{id}", s.getOrder).Methods("GET")

	// Настройка CORS
	r.Use(mux.CORSMethodMiddleware(r))
	r.Use(muxMiddleware)

	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./")))

	fmt.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

func muxMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
		return
		}
		next.ServeHTTP(w, r)
	})
}