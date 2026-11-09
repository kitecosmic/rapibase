// Package geo convierte las geometrías de PostGIS a GeoJSON.
//
// Postgres devuelve una columna geometry o geography en su salida de texto
// como EWKB en hexadecimal ("0101000020E6…"), que no le sirve a ningún
// cliente. PostGIS es una extensión, así que sus tipos no tienen un OID fijo
// y pgx no los conoce: llegan como string. Quien los recibe (REST, RPC, SQL,
// functions, realtime) llama a FromHexEWKB y, si el string es un EWKB
// completo y válido, lo devuelve como GeoJSON (RFC 7946).
//
// Solo se intenta con columnas de tipos definidos por el usuario (OID >=
// FirstUserOID): un text nunca se reinterpreta, aunque contenga hex.
package geo

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
)

// FirstUserOID: los OIDs por debajo son tipos del sistema (text, int, json…).
// Las extensiones como PostGIS crean los suyos por encima.
const FirstUserOID = 16384

const (
	flagZ    = 0x80000000
	flagM    = 0x40000000
	flagSRID = 0x20000000
)

var typeNames = map[uint32]string{
	1: "Point", 2: "LineString", 3: "Polygon",
	4: "MultiPoint", 5: "MultiLineString", 6: "MultiPolygon",
	7: "GeometryCollection",
}

// FromHexEWKB devuelve el GeoJSON de un EWKB (o WKB ISO) en hex. ok=false si
// s no es exactamente una geometría que GeoJSON pueda representar (curvas,
// superficies poliédricas, TIN…): en ese caso se deja el valor como estaba.
func FromHexEWKB(s string) (map[string]any, bool) {
	// el mínimo es un punto: 1 + 4 + 16 bytes
	if len(s) < 42 || len(s)%2 != 0 || (s[0] != '0') || (s[1] != '0' && s[1] != '1') {
		return nil, false
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, false
	}
	r := &reader{b: b}
	g, err := r.geometry(true)
	if err != nil || r.pos != len(b) {
		return nil, false
	}
	return g, true
}

type reader struct {
	b   []byte
	pos int
	ord binary.ByteOrder
}

var errShort = errors.New("ewkb: truncado")

func (r *reader) u32() (uint32, error) {
	if r.pos+4 > len(r.b) {
		return 0, errShort
	}
	v := r.ord.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) f64() (float64, error) {
	if r.pos+8 > len(r.b) {
		return 0, errShort
	}
	v := math.Float64frombits(r.ord.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v, nil
}

// header lee el orden de bytes y el tipo. Devuelve el tipo base y cuántas
// coordenadas trae cada punto, y si se debe conservar la Z.
func (r *reader) header(top bool) (base uint32, dims int, hasZ bool, err error) {
	if r.pos >= len(r.b) {
		return 0, 0, false, errShort
	}
	switch r.b[r.pos] {
	case 0:
		r.ord = binary.BigEndian
	case 1:
		r.ord = binary.LittleEndian
	default:
		return 0, 0, false, errors.New("ewkb: orden de bytes inválido")
	}
	r.pos++
	t, err := r.u32()
	if err != nil {
		return 0, 0, false, err
	}
	hasZ, hasM := t&flagZ != 0, t&flagM != 0
	if t&flagSRID != 0 {
		if !top {
			// PostGIS solo pone el SRID en la geometría de afuera
			return 0, 0, false, errors.New("ewkb: SRID anidado")
		}
		if _, err := r.u32(); err != nil {
			return 0, 0, false, err
		}
	}
	base = t &^ (flagZ | flagM | flagSRID)
	// WKB ISO: 1001 = Point Z, 2001 = Point M, 3001 = Point ZM
	switch base / 1000 {
	case 0:
	case 1:
		hasZ = true
	case 2:
		hasM = true
	case 3:
		hasZ, hasM = true, true
	default:
		return 0, 0, false, errors.New("ewkb: tipo desconocido")
	}
	base %= 1000
	if _, ok := typeNames[base]; !ok {
		return 0, 0, false, errors.New("ewkb: tipo sin equivalente en GeoJSON")
	}
	dims = 2
	if hasZ {
		dims++
	}
	if hasM {
		dims++
	}
	return base, dims, hasZ, nil
}

// point lee una coordenada. GeoJSON no tiene M: se descarta.
func (r *reader) point(dims int, hasZ bool) ([]float64, error) {
	c := make([]float64, 0, 3)
	for i := 0; i < dims; i++ {
		v, err := r.f64()
		if err != nil {
			return nil, err
		}
		if i < 2 || (i == 2 && hasZ) {
			c = append(c, v)
		}
	}
	return c, nil
}

func (r *reader) points(dims int, hasZ bool) ([][]float64, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int(n) > (len(r.b)-r.pos)/(8*dims) {
		return nil, errShort
	}
	out := make([][]float64, 0, n)
	for i := uint32(0); i < n; i++ {
		p, err := r.point(dims, hasZ)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *reader) rings(dims int, hasZ bool) ([][][]float64, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int(n) > (len(r.b)-r.pos)/4 {
		return nil, errShort
	}
	out := make([][][]float64, 0, n)
	for i := uint32(0); i < n; i++ {
		ring, err := r.points(dims, hasZ)
		if err != nil {
			return nil, err
		}
		out = append(out, ring)
	}
	return out, nil
}

// coords lee el cuerpo de una geometría simple (sin colecciones).
func (r *reader) coords(base uint32, dims int, hasZ bool) (any, error) {
	switch base {
	case 1:
		p, err := r.point(dims, hasZ)
		if err != nil {
			return nil, err
		}
		// POINT EMPTY se codifica con NaN
		if math.IsNaN(p[0]) && math.IsNaN(p[1]) {
			return []float64{}, nil
		}
		return p, nil
	case 2:
		return r.points(dims, hasZ)
	case 3:
		return r.rings(dims, hasZ)
	}
	return nil, errors.New("ewkb: tipo inesperado")
}

func (r *reader) geometry(top bool) (map[string]any, error) {
	base, dims, hasZ, err := r.header(top)
	if err != nil {
		return nil, err
	}
	name := typeNames[base]
	if base <= 3 {
		c, err := r.coords(base, dims, hasZ)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": name, "coordinates": c}, nil
	}

	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int(n) > len(r.b)-r.pos {
		return nil, errShort
	}
	if base == 7 {
		geoms := make([]any, 0, n)
		for i := uint32(0); i < n; i++ {
			g, err := r.geometry(false)
			if err != nil {
				return nil, err
			}
			geoms = append(geoms, g)
		}
		return map[string]any{"type": name, "geometries": geoms}, nil
	}

	// Multi*: cada parte es una geometría con su propio encabezado, del
	// tipo simple correspondiente (MultiPoint → Point…).
	parts := make([]any, 0, n)
	for i := uint32(0); i < n; i++ {
		pb, pdims, pz, err := r.header(false)
		if err != nil {
			return nil, err
		}
		if pb != base-3 {
			return nil, errors.New("ewkb: parte de tipo inesperado")
		}
		c, err := r.coords(pb, pdims, pz)
		if err != nil {
			return nil, err
		}
		parts = append(parts, c)
	}
	return map[string]any{"type": name, "coordinates": parts}, nil
}

// IsGeoJSON dice si v tiene forma de geometría GeoJSON. Se usa al escribir:
// un objeto así, dirigido a una columna geometry/geography, se guarda con
// ST_GeomFromGeoJSON.
func IsGeoJSON(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	t, _ := m["type"].(string)
	switch t {
	case "Point", "LineString", "Polygon", "MultiPoint", "MultiLineString", "MultiPolygon":
		_, ok := m["coordinates"]
		return ok
	case "GeometryCollection":
		_, ok := m["geometries"]
		return ok
	}
	return false
}
