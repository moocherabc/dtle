package mysql

import (
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	usql "udup/internal/client/driver/mysql/sql"
	"udup/internal/config"
	log "udup/internal/logger"
)

const (
	defaultChunkSize = 1000
)

var (
	stringOfBackslashAndQuoteChars = "\u005c\u00a5\u0160\u20a9\u2216\ufe68uff3c\u0022\u0027\u0060\u00b4\u02b9\u02ba\u02bb\u02bc\u02c8\u02ca\u02cb\u02d9\u0300\u0301\u2018\u2019\u201a\u2032\u2035\u275b\u275c\uff07"
)

type dumper struct {
	logger         *log.Entry
	chunkSize      int
	TableSchema    string
	TableName      string
	entriesCount   int
	resultsChannel chan *dumpEntry

	// DB is safe for using in goroutines
	// http://golang.org/src/database/sql/sql.go?s=5574:6362#L201
	db *sql.DB
}

func NewDumper(db *sql.DB, dbName, tableName string, logger *log.Entry) *dumper {
	dumper := &dumper{
		logger:         logger,
		db:             db,
		TableSchema:    dbName,
		TableName:      tableName,
		resultsChannel: make(chan *dumpEntry),
		chunkSize:      defaultChunkSize,
	}

	return dumper
}

func (d *dumper) getRowsCount() (uint64, error) {
	var res sql.NullString
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s.%s`,
		usql.EscapeName(d.TableSchema),
		usql.EscapeName(d.TableName),
	)
	err := d.db.QueryRow(query).Scan(&res)
	if err != nil {
		return 0, err
	}

	val, err := res.Value()
	if err != nil {
		return 0, err
	}

	return strconv.ParseUint(val.(string), 0, 64)
}

type dumpEntry struct {
	SystemVariablesStatement string
	DbSQL                    string
	TbSQL                    string
	Values                   string
	RowsCount                uint64
	Offset                   uint64
	Counter                  uint64
	data_text                []string
	colBuffer                *bytes.Buffer
	err                      error
}

func (j *dumpEntry) incrementCounter() {
	j.Counter++
}

func (d *dumper) getDumpEntries(systemVariablesStatement, dbSQL, tbSQL string) ([]*dumpEntry, error) {
	total, err := d.getRowsCount()
	if err != nil {
		return nil, err
	}

	sliceCount := int(math.Ceil(float64(total) / float64(d.chunkSize)))
	if sliceCount == 0 {
		sliceCount = 1
	}
	entries := make([]*dumpEntry, sliceCount)
	for i := 0; i < sliceCount; i++ {
		offset := uint64(i) * uint64(d.chunkSize)
		entries[i] = &dumpEntry{
			SystemVariablesStatement: systemVariablesStatement,
			DbSQL:     dbSQL,
			TbSQL:     tbSQL,
			RowsCount: total,
			Offset:    offset,
			Counter:   0}
	}
	return entries, nil
}

// dumps a specific chunk, reading chunk info from the channel
func (d *dumper) getChunkData(entry *dumpEntry) error {
	if entry.RowsCount == 0 {
		return nil
	}
	query := fmt.Sprintf(`SELECT * FROM %s.%s LIMIT %d OFFSET %d`,
		usql.EscapeName(d.TableSchema),
		usql.EscapeName(d.TableName),
		d.chunkSize,
		entry.Offset,
	)
	rows, err := d.db.Query(query)
	if err != nil {
		return err
	}

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	columnsCount := len(columns)
	values := make([]sql.RawBytes, columnsCount)

	scanArgs := make([]interface{}, len(values))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	entry.data_text = make([]string, 0)
	for rows.Next() {
		err = rows.Scan(scanArgs...)
		if err != nil {
			return err
		}

		var value string
		dataStrings := make([]string, columnsCount)
		for i, col := range values {
			// Here we can check if the value is nil (NULL value)
			if col == nil {
				value = "NULL"
			} else {
				colValue := string(col)
				if !needsQuoting(colValue) {
					value = colValue
				} else {
					entry.colBuffer = new(bytes.Buffer)
					for _, char_c := range colValue {
						c := fmt.Sprintf("%c", char_c)
						if needsQuoting(c) {
							entry.colBuffer.WriteString("\\")
						}
						entry.colBuffer.WriteString(c)
					}
					value = entry.colBuffer.String()
				}
				value = fmt.Sprintf("'%s'", value)
			}
			dataStrings[i] = value
		}
		entry.data_text = append(entry.data_text, "("+strings.Join(dataStrings, ",")+")")
		entry.incrementCounter()
	}
	query = fmt.Sprintf(`
			insert into %s.%s
				(%s)
			values
				%s
			on duplicate key update
				%s=VALUES(%s)
		`,
		usql.EscapeName(d.TableSchema),
		usql.EscapeName(d.TableName),
		strings.Join(columns, ","),
		strings.Join(entry.data_text, ","),
		columns[0],
		columns[0],
	)
	entry.Values = query
	return nil
}

func needsQuoting(s string) bool {
	if strings.ContainsAny(s, stringOfBackslashAndQuoteChars) {
		return true
	}
	return false
}

func (d *dumper) worker(entriesChannel <-chan *dumpEntry) {
	for e := range entriesChannel {
		err := d.getChunkData(e)
		if err != nil {
			e.err = err
		}
		d.resultsChannel <- e
	}
}

func (d *dumper) Dump(systemVariablesStatement string, w int) error {
	dbSQL := fmt.Sprintf("CREATE DATABASE %s", d.TableSchema)
	tbSQL, err := d.createTableSQL()
	if err != nil {
		return err
	}
	entries, err := d.getDumpEntries(systemVariablesStatement, dbSQL, tbSQL)
	if err != nil {
		return err
	}

	workersCount := int(math.Min(float64(w), float64(len(entries))))
	if workersCount < 1 {
		return nil
	}
	d.entriesCount = len(entries)

	entriesChannel := make(chan *dumpEntry)
	for i := 0; i < workersCount; i++ {
		go d.worker(entriesChannel)
	}

	go func() {
		for _, e := range entries {
			entriesChannel <- e
		}
		close(entriesChannel)
	}()

	return nil
}

//LOCK TABLES {{ .Name }} WRITE;
//INSERT INTO {{ .Name }} VALUES {{ .Values }};
//UNLOCK TABLES;

func showDatabases(db *sql.DB) ([]string, error) {
	dbs := make([]string, 0)

	// Get table list
	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		return dbs, err
	}
	defer rows.Close()

	// Read result
	for rows.Next() {
		var database sql.NullString
		if err := rows.Scan(&database); err != nil {
			return dbs, err
		}
		switch strings.ToLower(database.String) {
		case "sys", "mysql", "information_schema", "performance_schema":
			continue
		default:
			dbs = append(dbs, database.String)
		}
	}
	return dbs, rows.Err()
}

func showTables(db *sql.DB, dbName string) (tables []*config.Table, err error) {
	// Get table list
	rows, err := db.Query(fmt.Sprintf("SHOW TABLES IN %s", dbName))
	if err != nil {
		return tables, err
	}
	defer rows.Close()

	// Read result
	for rows.Next() {
		var table sql.NullString
		if err := rows.Scan(&table); err != nil {
			return tables, err
		}
		tb := &config.Table{TableSchema: dbName, TableName: table.String}
		tables = append(tables, tb)
	}
	return tables, rows.Err()
}

func (d *dumper) createTableSQL() (string, error) {
	query := fmt.Sprintf(`SHOW CREATE TABLE %s.%s`,
		usql.EscapeName(d.TableSchema),
		usql.EscapeName(d.TableName),
	)
	// Get table creation SQL
	var table_return sql.NullString
	var table_sql sql.NullString
	err := d.db.QueryRow(query).Scan(&table_return, &table_sql)
	if err != nil {
		return "", err
	}
	if table_return.String != d.TableName {
		return "", fmt.Errorf("Returned table is not the same as requested table")
	}

	return fmt.Sprintf("USE %s;%s", d.TableSchema, table_sql.String), nil
}
