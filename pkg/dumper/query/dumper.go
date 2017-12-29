package query

import (
	"fmt"
	"io"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/hellofresh/klepto/pkg/config"
	"github.com/hellofresh/klepto/pkg/database"
	"github.com/hellofresh/klepto/pkg/dumper"
	"github.com/hellofresh/klepto/pkg/reader"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// textDumper dumps a database's structure to a stream
type textDumper struct {
	reader reader.Reader
	output io.Writer
}

// NewDumper is the constructor for MySQLDumper
func NewDumper(output io.Writer, rdr reader.Reader) dumper.Dumper {
	return &textDumper{
		reader: rdr,
		output: output,
	}
}

func (d *textDumper) Dump(done chan<- struct{}, configTables config.Tables) error {
	tables, err := d.reader.GetTables()
	if err != nil {
		return errors.Wrap(err, "could not get tables")
	}

	structure, err := d.reader.GetStructure()
	if err != nil {
		return errors.Wrap(err, "could not get database structure")
	}
	io.WriteString(d.output, structure)

	for _, tbl := range tables {
		var opts reader.ReadTableOpt

		table, err := configTables.FindByName(tbl)
		if err != nil {
			log.WithError(err).WithField("table", tbl).Debug("no configuration found for table")
		}

		// Create read/write chanel
		rowChan := make(chan database.Row)

		if table != nil {
			opts = reader.ReadTableOpt{
				Limit:         table.Filter.Limit,
				Relationships: d.relationshipConfigToOptions(table.Relationships),
			}
		}

		go d.reader.ReadTable(tbl, rowChan, opts)

		go func(tableName string) {
			for {
				row, more := <-rowChan
				if !more {
					done <- struct{}{}
					return
				}

				insert := sq.Insert(tableName).SetMap(d.toSQLColumnMap(row))
				io.WriteString(d.output, sq.DebugSqlizer(insert))
				io.WriteString(d.output, "\n")
			}
		}(tbl)
	}

	return nil
}

func (d *textDumper) toSQLColumnMap(row database.Row) map[string]interface{} {
	sqlColumnMap := make(map[string]interface{})

	for column, value := range row {
		strValue, err := d.toSQLStringValue(value)
		if err != nil {
			log.WithError(err).Debug("could not convert value to string")
		}

		sqlColumnMap[column] = fmt.Sprintf("%v", strValue)
	}

	return sqlColumnMap
}

// ResolveType accepts a value and attempts to determine its type
func (d *textDumper) toSQLStringValue(src interface{}) (string, error) {
	switch src.(type) {
	case int64:
		if value, ok := src.(int64); ok {
			return strconv.FormatInt(value, 10), nil
		}
	case float64:
		if value, ok := src.(float64); ok {
			return fmt.Sprintf("%v", value), nil
		}
	case bool:
		if value, ok := src.(bool); ok {
			return strconv.FormatBool(value), nil
		}
	case string:
		if value, ok := src.(string); ok {
			return value, nil
		}
	case []byte:
		// TODO handle blobs?
		if value, ok := src.([]byte); ok {
			return string(value), nil
		}
	case time.Time:
		if value, ok := src.(time.Time); ok {
			return value.String(), nil
		}
	case nil:
		return "NULL", nil
	case *interface{}:
		if src == nil {
			return "NULL", nil
		}
		return d.toSQLStringValue(*(src.(*interface{})))
	default:
		return "", errors.New("could not parse type")
	}

	return "", nil
}

func (d *textDumper) relationshipConfigToOptions(relationshipsConfig []*config.Relationship) []*reader.RelationshipOpt {
	var opts []*reader.RelationshipOpt

	for _, r := range relationshipsConfig {
		opts = append(opts, &reader.RelationshipOpt{
			ReferencedTable: r.ReferencedTable,
			ReferencedKey:   r.ReferencedKey,
			ForeignKey:      r.ForeignKey,
		})
	}

	return opts
}
