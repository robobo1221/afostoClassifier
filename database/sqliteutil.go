package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	dbPath        = "./test.db"
	migrationPath = "./migrations"
	dbInstance    *sql.DB
	once          sync.Once
)

// InitSqlite initializes the SQLite database with necessary configurations.
// It ensures that only one instance of *sql.DB is created using sync.Once.
func InitSqlite() {
	var err error
	once.Do(func() {
		// Data Source Name (DSN) with configurations:
		// - Enable Foreign Keys
		// - Set Journal Mode to WAL for better concurrency
		// - Set Busy Timeout to 5000 milliseconds
		dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000", dbPath)
		dbInstance, err = sql.Open("sqlite", dsn)
		if err != nil {
			log.Fatalf("Failed to open database: %v", err)
		}

		// Limit the number of open connections to 1 to prevent lock contention
		dbInstance.SetMaxOpenConns(1)

		// Verify the connection
		if err = dbInstance.Ping(); err != nil {
			log.Fatalf("Failed to ping database: %v", err)
		}
	})
}

// Migrate applies all SQL migration files to the database.
// It ensures that migrations are applied using the persistent dbInstance.
func Migrate() {
	InitSqlite()

	// Read migration files
	files, err := os.ReadDir(migrationPath)
	if err != nil {
		log.Fatalf("Failed to read migration directory: %v", err)
	}

	// Apply each migration
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fmt.Println("Migrating:", file.Name())
		migration, err := os.ReadFile(fmt.Sprintf("%s/%s", migrationPath, file.Name()))
		if err != nil {
			log.Fatalf("Failed to read migration file %s: %v", file.Name(), err)
		}

		// Execute migration within a transaction
		tx, err := dbInstance.Begin()
		if err != nil {
			log.Fatalf("Failed to begin transaction for migration %s: %v", file.Name(), err)
		}

		_, err = tx.Exec(string(migration))
		if err != nil {
			tx.Rollback()
			log.Fatalf("Failed to execute migration %s: %v", file.Name(), err)
		}

		if err = tx.Commit(); err != nil {
			log.Fatalf("Failed to commit migration %s: %v", file.Name(), err)
		}
	}
}

// InsertConnectionWithPsqr inserts or updates a connection with associated PSQR data.
// It uses the persistent dbInstance and handles concurrency appropriately.
func InsertConnectionWithPsqr(
	connection string,
	perc float64,
	count int,
	q0, q1, q2, q3, q4 float64,
	n0, n1, n2, n3, n4 int,
	np0, np1, np2, np3, np4 float64,
	dn0, dn1, dn2, dn3, dn4 float64,
) {
	InitSqlite()

	// Use a transaction to ensure atomicity
	tx, err := dbInstance.Begin()
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Check if connection already exists
	var psqrId int
	query := fmt.Sprintf("SELECT currentPsqr%dId FROM connection WHERE connectionOrigin = ?", int(perc*100))

	err = tx.QueryRow(query, connection).Scan(&psqrId)
	if err == nil {
		// Connection exists, update the PSQR
		UpdatePsqrWithTx(tx, psqrId, perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4)
		if err = tx.Commit(); err != nil {
			log.Fatalf("Failed to commit transaction: %v", err)
		}
		return
	} else if err != sql.ErrNoRows {
		log.Fatalf("Failed to query connection: %v", err)
	}

	// Insert into psqr and get the inserted ID
	res, err := tx.Exec(
		"INSERT INTO psqr (perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4,
	)
	if err != nil {
		log.Fatalf("Failed to insert into psqr: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		log.Fatalf("Failed to get last insert ID: %v", err)
	}

	// Insert into connection
	query = fmt.Sprintf("INSERT INTO connection (connectionOrigin, currentPsqr%dId) VALUES (?, ?)", int(perc*100))
	_, err = tx.Exec(query, connection, id)
	if err != nil {
		log.Fatalf("Failed to insert into connection: %v", err)
	}

	if err = tx.Commit(); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}
}

func UpdatePsqr(
	id int,
	perc float64,
	count int,
	q0, q1, q2, q3, q4 float64,
	n0, n1, n2, n3, n4 int,
	np0, np1, np2, np3, np4 float64,
	dn0, dn1, dn2, dn3, dn4 float64,
) {
	InitSqlite()

	_, err := dbInstance.Exec(
		"UPDATE psqr SET perc = ?, count = ?, q0 = ?, q1 = ?, q2 = ?, q3 = ?, q4 = ?, n0 = ?, n1 = ?, n2 = ?, n3 = ?, n4 = ?, np0 = ?, np1 = ?, np2 = ?, np3 = ?, np4 = ?, dn0 = ?, dn1 = ?, dn2 = ?, dn3 = ?, dn4 = ? WHERE id = ?",
		perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4, id,
	)
	if err != nil {
		log.Fatalf("Failed to update psqr: %v", err)
	}
}

// UpdatePsqr updates an existing PSQR record.
// It accepts a transaction to ensure operations are part of a larger atomic action.
func UpdatePsqrWithTx(
	tx *sql.Tx,
	id int,
	perc float64,
	count int,
	q0, q1, q2, q3, q4 float64,
	n0, n1, n2, n3, n4 int,
	np0, np1, np2, np3, np4 float64,
	dn0, dn1, dn2, dn3, dn4 float64,
) {
	_, err := tx.Exec(
		"UPDATE psqr SET perc = ?, count = ?, q0 = ?, q1 = ?, q2 = ?, q3 = ?, q4 = ?, n0 = ?, n1 = ?, n2 = ?, n3 = ?, n4 = ?, np0 = ?, np1 = ?, np2 = ?, np3 = ?, np4 = ?, dn0 = ?, dn1 = ?, dn2 = ?, dn3 = ?, dn4 = ? WHERE id = ?",
		perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4, id,
	)
	if err != nil {
		log.Fatalf("Failed to update psqr: %v", err)
	}
}

// SetNewPsqr sets a new PSQR for a given connection.
// It uses the persistent dbInstance and handles concurrency appropriately.
func SetNewPsqr(connection string, id int, perc float64) int {
	InitSqlite()

	_, err := dbInstance.Exec(
		fmt.Sprintf("UPDATE connection SET currentPsqr%dId = ? WHERE connectionOrigin = ?", int(perc*100)),
		id, connection,
	)
	if err != nil {
		log.Fatalf("Failed to set new PSQR: %v", err)
	}

	return id
}

// SetPreviousPsqr sets the previous PSQR ID for a given PSQR record.
// It uses the persistent dbInstance and handles concurrency appropriately.
func SetPreviousPsqr(newCurrentId int, oldCurrentId int) int {
	InitSqlite()

	_, err := dbInstance.Exec("UPDATE psqr SET previousPsqrId = ? WHERE id = ?", oldCurrentId, newCurrentId)
	if err != nil {
		log.Fatalf("Failed to set previous PSQR: %v", err)
	}

	return newCurrentId
}

// GetPsqr retrieves a PSQR record by its ID.
// It uses the persistent dbInstance and handles concurrency appropriately.
func GetPsqr(id int) (int, any, float64, int, [5]float64, [5]int, [5]float64, [5]float64) {
	InitSqlite()

	// Define variables to hold the data
	var foundId int
	var previousPsqrId any
	var foundPerc float64
	var count int
	var q [5]float64
	var n [5]int
	var np [5]float64
	var dn [5]float64

	// Query the psqr table
	row := dbInstance.QueryRow("SELECT id, previousPsqrId, perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4 FROM psqr WHERE id = ?", id)
	err := row.Scan(
		&foundId,
		&previousPsqrId,
		&foundPerc,
		&count,
		&q[0], &q[1], &q[2], &q[3], &q[4],
		&n[0], &n[1], &n[2], &n[3], &n[4],
		&np[0], &np[1], &np[2], &np[3], &np[4],
		&dn[0], &dn[1], &dn[2], &dn[3], &dn[4],
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil, 0, 0, [5]float64{}, [5]int{}, [5]float64{}, [5]float64{}
		}
		log.Fatalf("Failed to get psqr: %v", err)
	}

	return foundId, previousPsqrId, foundPerc, count, q, n, np, dn
}

// GetPsqrFromConnection retrieves the PSQR associated with a given connection and percentage.
// It uses the persistent dbInstance and handles concurrency appropriately.
func GetPsqrFromConnection(
	connection string,
	perc float64,
) (int, any, float64, int, [5]float64, [5]int, [5]float64, [5]float64) {
	InitSqlite()

	var psqrId int
	query := fmt.Sprintf("SELECT currentPsqr%dId FROM connection WHERE connectionOrigin = ?", int(perc*100))
	err := dbInstance.QueryRow(query, connection).Scan(&psqrId)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil, 0, 0, [5]float64{}, [5]int{}, [5]float64{}, [5]float64{}
		}
		log.Fatalf("Failed to get PSQR from connection: %v", err)
	}

	return GetPsqr(psqrId)
}

// CreatePsqr inserts a new PSQR record and returns its ID.
// It uses the persistent dbInstance and handles concurrency appropriately.
func CreatePsqr(
	perc float64,
	count int,
	q0, q1, q2, q3, q4 float64,
	n0, n1, n2, n3, n4 int,
	np0, np1, np2, np3, np4 float64,
	dn0, dn1, dn2, dn3, dn4 float64,
) int {
	InitSqlite()

	res, err := dbInstance.Exec(
		"INSERT INTO psqr (perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4,
	)
	if err != nil {
		log.Fatalf("Failed to create PSQR: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		log.Fatalf("Failed to get last insert ID for PSQR: %v", err)
	}

	return int(id)
}

// SwapPsqr creates a new PSQR, updates the connection to point to the new PSQR,
// sets the previous PSQR, and deletes the old PSQR if necessary.
// It uses transactions to ensure atomicity.
func SwapPsqr(connection string, perc float64) int {
	InitSqlite()

	// Start a transaction
	tx, err := dbInstance.Begin()
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Get the current PSQR from the connection
	id, previousId, currentPerc, _, q, n, np, dn := GetPsqrFromConnectionTransactional(tx, connection, perc)

	if currentPerc == 0 {
		return -1
	}

	// Create a new PSQR
	newId := CreatePsqrTransactional(tx, currentPerc, 0, q, n, np, dn)

	// Update the connection to point to the new PSQR
	SetNewPsqrTransactional(tx, connection, newId, currentPerc)

	// Set the previous PSQR of the new PSQR to the old PSQR
	SetPreviousPsqrTransactional(tx, newId, id)

	// Delete the old PSQR if it exists
	if previousId != nil {
		_, err = tx.Exec("DELETE FROM psqr WHERE id = ?", previousId)
		if err != nil {
			log.Fatalf("Failed to delete previous PSQR: %v", err)
		}
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}

	return newId
}

// Below are helper functions that operate within a transaction.
// These ensure that operations are atomic and reduce lock contention.

// GetPsqrFromConnectionTransactional retrieves the PSQR within a transaction.
func GetPsqrFromConnectionTransactional(tx *sql.Tx, connection string, perc float64) (int, any, float64, int, [5]float64, [5]int, [5]float64, [5]float64) {
	var psqrId int
	query := fmt.Sprintf("SELECT currentPsqr%dId FROM connection WHERE connectionOrigin = ?", int(perc*100))
	err := tx.QueryRow(query, connection).Scan(&psqrId)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil, 0, 0, [5]float64{}, [5]int{}, [5]float64{}, [5]float64{}
		}
		log.Fatalf("Failed to get PSQR from connection within transaction: %v", err)
	}

	return GetPsqrTransactional(tx, psqrId)
}

// GetPsqrTransactional retrieves a PSQR within a transaction.
func GetPsqrTransactional(tx *sql.Tx, id int) (int, any, float64, int, [5]float64, [5]int, [5]float64, [5]float64) {
	var foundId int
	var previousPsqrId any
	var foundPerc float64
	var count int
	var q [5]float64
	var n [5]int
	var np [5]float64
	var dn [5]float64

	row := tx.QueryRow("SELECT id, previousPsqrId, perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4 FROM psqr WHERE id = ?", id)
	err := row.Scan(
		&foundId,
		&previousPsqrId,
		&foundPerc,
		&count,
		&q[0], &q[1], &q[2], &q[3], &q[4],
		&n[0], &n[1], &n[2], &n[3], &n[4],
		&np[0], &np[1], &np[2], &np[3], &np[4],
		&dn[0], &dn[1], &dn[2], &dn[3], &dn[4],
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil, 0, 0, [5]float64{}, [5]int{}, [5]float64{}, [5]float64{}
		}
		log.Fatalf("Failed to get PSQR within transaction: %v", err)
	}

	return foundId, previousPsqrId, foundPerc, count, q, n, np, dn
}

// CreatePsqrTransactional creates a PSQR within a transaction.
func CreatePsqrTransactional(tx *sql.Tx, perc float64, count int, q [5]float64, n [5]int, np [5]float64, dn [5]float64) int {
	res, err := tx.Exec(
		"INSERT INTO psqr (perc, count, q0, q1, q2, q3, q4, n0, n1, n2, n3, n4, np0, np1, np2, np3, np4, dn0, dn1, dn2, dn3, dn4) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		perc, count, q[0], q[1], q[2], q[3], q[4],
		n[0], n[1], n[2], n[3], n[4],
		np[0], np[1], np[2], np[3], np[4],
		dn[0], dn[1], dn[2], dn[3], dn[4],
	)
	if err != nil {
		log.Fatalf("Failed to create PSQR within transaction: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		log.Fatalf("Failed to get last insert ID for PSQR within transaction: %v", err)
	}

	return int(id)
}

// SetNewPsqrTransactional sets a new PSQR within a transaction.
func SetNewPsqrTransactional(tx *sql.Tx, connection string, id int, perc float64) {
	_, err := tx.Exec(
		fmt.Sprintf("UPDATE connection SET currentPsqr%dId = ? WHERE connectionOrigin = ?", int(perc*100)),
		id, connection,
	)
	if err != nil {
		log.Fatalf("Failed to set new PSQR within transaction: %v", err)
	}
}

// SetPreviousPsqrTransactional sets the previous PSQR within a transaction.
func SetPreviousPsqrTransactional(tx *sql.Tx, newCurrentId int, oldCurrentId int) {
	_, err := tx.Exec("UPDATE psqr SET previousPsqrId = ? WHERE id = ?", oldCurrentId, newCurrentId)
	if err != nil {
		log.Fatalf("Failed to set previous PSQR within transaction: %v", err)
	}
}
