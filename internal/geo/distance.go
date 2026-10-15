package geo

import "math"

// HaversineMeters returns the great-circle distance between two WGS84 points.
func HaversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusM = 6_371_000.0
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) + math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * earthRadiusM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
