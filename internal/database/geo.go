package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rapibase/rapibase/internal/geo"
)

// geoColumns devuelve las columnas geometry/geography de la tabla, pero
// solo si alguno de los valores a escribir tiene forma de GeoJSON: una
// escritura normal no paga la consulta al catálogo.
func geoColumns(ctx context.Context, q Querier, table string, data map[string]interface{}) (map[string]bool, error) {
	found := false
	for _, v := range data {
		if geo.IsGeoJSON(v) {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	rows, err := q.Query(ctx, `SELECT a.attname FROM pg_attribute a JOIN pg_type t ON t.oid = a.atttypid
		WHERE a.attrelid = to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped
		AND t.typname IN ('geometry', 'geography')`, quoteIdentifier(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// geoParam arma el placeholder $n de una columna. Un GeoJSON dirigido a una
// columna de PostGIS entra por ST_GeomFromGeoJSON (SRID 4326, el de
// GeoJSON); el cast a geography es implícito. Lo demás —incluido un objeto
// con forma de GeoJSON que va a una columna jsonb— pasa tal cual.
func geoParam(geoCols map[string]bool, col string, val interface{}, i int) (string, interface{}) {
	if geoCols[col] && geo.IsGeoJSON(val) {
		b, err := json.Marshal(val)
		if err == nil {
			return fmt.Sprintf("ST_GeomFromGeoJSON($%d::text)", i), string(b)
		}
	}
	return fmt.Sprintf("$%d", i), val
}
