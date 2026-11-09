package geo

import (
	"encoding/json"
	"testing"
)

// Los hex salen de un PostGIS 3 real (::text) y el GeoJSON esperado de
// ST_AsGeoJSON sobre la misma geometría.
func TestFromHexEWKB(t *testing.T) {
	casos := []struct{ nombre, hex, want string }{
		{"punto con SRID (geography)",
			"0101000020E6100000713D0AD7A3304DC0CDCCCCCCCC4C41C0",
			`{"coordinates":[-58.38,-34.6],"type":"Point"}`},
		{"punto Z",
			"0101000080000000000000F03F00000000000000400000000000000840",
			`{"coordinates":[1,2,3],"type":"Point"}`},
		{"punto M (GeoJSON no tiene M)",
			"0101000040000000000000F03F00000000000000400000000000002240",
			`{"coordinates":[1,2],"type":"Point"}`},
		{"punto vacío",
			"0101000000000000000000F87F000000000000F87F",
			`{"coordinates":[],"type":"Point"}`},
		{"polígono con agujero",
			"0103000020E610000002000000040000000000000000000000000000000000000000000000000010400000000000000000000000000000104000000000000010400000000000000000000000000000000004000000000000000000F03F000000000000F03F0000000000000040000000000000F03F00000000000000400000000000000040000000000000F03F000000000000F03F",
			`{"coordinates":[[[0,0],[4,0],[4,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]],"type":"Polygon"}`},
		{"multipolígono",
			"0106000020E6100000020000000103000000010000000400000000000000000000000000000000000000000000000000F03F0000000000000000000000000000F03F000000000000F03F000000000000000000000000000000000103000000010000000400000000000000000014400000000000001440000000000000184000000000000014400000000000001840000000000000184000000000000014400000000000001440",
			`{"coordinates":[[[[0,0],[1,0],[1,1],[0,0]]],[[[5,5],[6,5],[6,6],[5,5]]]],"type":"MultiPolygon"}`},
		{"colección",
			"0107000000020000000101000000000000000000F03F000000000000004001020000000200000000000000000000000000000000000000000000000000F03F000000000000F03F",
			`{"geometries":[{"coordinates":[1,2],"type":"Point"},{"coordinates":[[0,0],[1,1]],"type":"LineString"}],"type":"GeometryCollection"}`},
	}
	for _, c := range casos {
		g, ok := FromHexEWKB(c.hex)
		if !ok {
			t.Errorf("%s: no se decodificó", c.nombre)
			continue
		}
		got, _ := json.Marshal(g)
		if string(got) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.nombre, got, c.want)
		}
	}
}

func TestFromHexEWKBRechaza(t *testing.T) {
	for _, s := range []string{
		"",
		"hola",
		"DEADBEEF",
		"0101000020E6100000713D0AD7A3304DC0CDCCCCCCCC4C41",     // truncado
		"0101000020E6100000713D0AD7A3304DC0CDCCCCCCCC4C41C000", // sobra un byte
		"0108000000",                   // CircularString: sin GeoJSON
		"0101000000000000000000F03FZZ", // no es hex
	} {
		if _, ok := FromHexEWKB(s); ok {
			t.Errorf("%q no debería decodificarse", s)
		}
	}
}

func TestIsGeoJSON(t *testing.T) {
	si := []any{
		map[string]any{"type": "Point", "coordinates": []any{1.0, 2.0}},
		map[string]any{"type": "GeometryCollection", "geometries": []any{}},
	}
	no := []any{
		"POINT(1 2)",
		map[string]any{"type": "Point"},
		map[string]any{"type": "Feature", "geometry": map[string]any{}},
		map[string]any{"coordinates": []any{1.0, 2.0}},
	}
	for _, v := range si {
		if !IsGeoJSON(v) {
			t.Errorf("%v debería ser GeoJSON", v)
		}
	}
	for _, v := range no {
		if IsGeoJSON(v) {
			t.Errorf("%v no debería ser GeoJSON", v)
		}
	}
}
