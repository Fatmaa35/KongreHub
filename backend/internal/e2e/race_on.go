//go:build race

package e2e

// RaceEnabled: -race ile derlendiğinde süre sınırları (ör. T-1 20 sn) uygulanmaz; doğruluk yine denetlenir.
const RaceEnabled = true
