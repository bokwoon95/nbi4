package nbi4

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/bokwoon95/sqddl/ddl"
)

//go:embed schema.json
var schemaJSON []byte

type Table struct {
	Table      string
	PrimaryKey []string
	Columns    []struct {
		Dialect   string
		Column    string
		Type      map[string]string
		Generated struct {
			Expression string
			Stored     bool
		}
		Index      bool
		PrimaryKey bool
		Unique     bool
		NotNull    bool
		References struct {
			Table  string
			Column string
		}
	}
	Indexes []struct {
		Dialect        string
		Type           string
		Unique         bool
		Columns        []string
		IncludeColumns []string
		Predicate      string
	}
	Constraints []struct {
		Dialect           string
		Type              string
		Columns           []string
		ReferencesTable   string
		ReferencesColumns []string
	}
}

// UnmarshalCatalog unmarshals a JSON payload into a *ddl.Catalog.
func UnmarshalCatalog(catalog *ddl.Catalog, b []byte, namespace string) error {
	prefix := ""
	if namespace != "" {
		prefix = namespace + "_"
	}
	var tables []Table
	decoder := json.NewDecoder(bytes.NewReader(b))
	err := decoder.Decode(&tables)
	if err != nil {
		return err
	}
	ddlCache := ddl.NewCatalogCache(catalog)
	ddlSchema := ddlCache.GetOrCreateSchema(catalog, "")
	for _, table := range tables {
		tableName := prefix + table.Table
		ddlTable := ddlCache.GetOrCreateTable(ddlSchema, tableName)
		if len(table.PrimaryKey) != 0 {
			ddlCache.AddOrUpdateConstraint(ddlTable, ddl.Constraint{
				ConstraintName: ddl.GenerateName(ddl.PRIMARY_KEY, tableName, table.PrimaryKey),
				ConstraintType: ddl.PRIMARY_KEY,
				Columns:        table.PrimaryKey,
			})
		}
		for _, column := range table.Columns {
			columnType := column.Type[catalog.Dialect]
			if columnType == "" {
				columnType = column.Type["default"]
			}
			if column.Dialect != "" && column.Dialect != catalog.Dialect {
				continue
			}
			ddlCache.AddOrUpdateColumn(ddlTable, ddl.Column{
				ColumnName:          column.Column,
				ColumnType:          columnType,
				IsPrimaryKey:        column.PrimaryKey,
				IsUnique:            column.Unique,
				IsNotNull:           column.NotNull,
				GeneratedExpr:       column.Generated.Expression,
				GeneratedExprStored: column.Generated.Stored,
			})
			if column.PrimaryKey {
				ddlCache.AddOrUpdateConstraint(ddlTable, ddl.Constraint{
					ConstraintName: ddl.GenerateName(ddl.PRIMARY_KEY, tableName, []string{column.Column}),
					ConstraintType: ddl.PRIMARY_KEY,
					Columns:        []string{column.Column},
				})
			}
			if column.Unique {
				ddlCache.AddOrUpdateConstraint(ddlTable, ddl.Constraint{
					ConstraintName: ddl.GenerateName(ddl.UNIQUE, tableName, []string{column.Column}),
					ConstraintType: ddl.UNIQUE,
					Columns:        []string{column.Column},
				})
			}
			if column.Index {
				ddlCache.AddOrUpdateIndex(ddlTable, ddl.Index{
					IndexName: ddl.GenerateName(ddl.INDEX, tableName, []string{column.Column}),
					Columns:   []string{column.Column},
				})
			}
			if column.References.Table != "" {
				columnName := column.References.Column
				if columnName == "" {
					columnName = column.Column
				}
				ddlCache.AddOrUpdateConstraint(ddlTable, ddl.Constraint{
					ConstraintName:    ddl.GenerateName(ddl.FOREIGN_KEY, tableName, []string{column.Column}),
					ConstraintType:    ddl.FOREIGN_KEY,
					Columns:           []string{column.Column},
					ReferencesTable:   column.References.Table,
					ReferencesColumns: []string{columnName},
					UpdateRule:        ddl.CASCADE,
				})
			}
		}
		for _, index := range table.Indexes {
			if index.Dialect != "" && index.Dialect != catalog.Dialect {
				continue
			}
			ddlCache.AddOrUpdateIndex(ddlTable, ddl.Index{
				IndexName:      ddl.GenerateName(ddl.INDEX, tableName, index.Columns),
				IndexType:      index.Type,
				IsUnique:       index.Unique,
				Columns:        index.Columns,
				IncludeColumns: index.IncludeColumns,
				Predicate:      index.Predicate,
			})
		}
		for _, constraint := range table.Constraints {
			if constraint.Dialect != "" && constraint.Dialect != catalog.Dialect {
				continue
			}
			if constraint.Type != ddl.PRIMARY_KEY && constraint.Type != ddl.FOREIGN_KEY && constraint.Type != ddl.UNIQUE {
				return fmt.Errorf("%s: invalid constraint type %q", tableName, constraint.Type)
			}
			ddlCache.AddOrUpdateConstraint(ddlTable, ddl.Constraint{
				ConstraintName:    ddl.GenerateName(constraint.Type, tableName, constraint.Columns),
				ConstraintType:    constraint.Type,
				Columns:           constraint.Columns,
				ReferencesTable:   constraint.ReferencesTable,
				ReferencesColumns: constraint.ReferencesColumns,
			})
		}
	}
	return nil
}
