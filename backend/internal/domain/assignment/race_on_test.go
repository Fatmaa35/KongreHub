//go:build race

package assignment

// raceEnabled: -race altında kod 10-50 kat yavaşlar; süre sınırı yalnızca normal derlemede ölçülür.
const raceEnabled = true
