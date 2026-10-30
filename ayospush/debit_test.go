package ayospush

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	whatsapp "github.com/bangaromaric/whatsapp-go"
)

// TestLireDebit fige la lecture des en-têtes de bridage.
//
// La règle que toute la table vérifie est la même : RIEN NE FAIT ÉCHOUER UN ENVOI. Une valeur
// absente, mal formée ou absurde rend zéro, et l'appelant retombe sur son propre repli. Le
// contraire serait la pire des conduites : refuser une réponse par ailleurs valide parce
// qu'une aide facultative était illisible.
func TestLireDebit(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	unix := func(d time.Duration) string {
		return strconv.FormatInt(maintenant.Add(d).Unix(), 10)
	}
	unixMilli := func(d time.Duration) string {
		return strconv.FormatInt(maintenant.Add(d).UnixMilli(), 10)
	}
	dateHTTP := func(d time.Duration) string {
		return maintenant.Add(d).UTC().Format(http.TimeFormat)
	}

	cas := []struct {
		nom     string
		entetes map[string]string
		want    whatsapp.Debit
	}{
		{
			nom:     "aucun en-tete, la passerelle n a rien dit",
			entetes: nil,
			want:    whatsapp.Debit{},
		},
		{
			nom:     "la limite mesuree le 13 octobre 2026",
			entetes: map[string]string{"X-RateLimit-Limit": "5"},
			want:    whatsapp.Debit{Limite: 5},
		},
		{
			nom:     "une limite nulle n est pas une limite",
			entetes: map[string]string{"X-RateLimit-Limit": "0"},
			want:    whatsapp.Debit{},
		},
		{
			nom:     "une limite illisible est ignoree",
			entetes: map[string]string{"X-RateLimit-Limit": "cinq"},
			want:    whatsapp.Debit{},
		},
		{
			nom:     "Retry-After en secondes",
			entetes: map[string]string{"Retry-After": "40"},
			want:    whatsapp.Debit{Reprise: 40 * time.Second},
		},
		{
			// La seconde forme que la norme autorise. Ne pas la lire ferait retomber sur un
			// delai devine la moitie du temps, sans que rien ne le signale.
			nom:     "Retry-After en date HTTP",
			entetes: map[string]string{"Retry-After": dateHTTP(2 * time.Minute)},
			want:    whatsapp.Debit{Reprise: 2 * time.Minute},
		},
		{
			nom:     "une date HTTP deja passee ne fait pas attendre",
			entetes: map[string]string{"Retry-After": dateHTTP(-time.Hour)},
			want:    whatsapp.Debit{},
		},
		{
			nom:     "un Retry-After negatif est ecarte",
			entetes: map[string]string{"Retry-After": "-30"},
			want:    whatsapp.Debit{},
		},
		{
			// Au dela de vingt-quatre heures, c'est une magnitude mal lue et non une
			// instruction : la garder parquerait une file entiere pour des annees.
			nom:     "un Retry-After demesure est ecarte",
			entetes: map[string]string{"Retry-After": "999999999"},
			want:    whatsapp.Debit{},
		},
		{
			// MESURE, et c'est la raison du plafond applique avant la multiplication.
			// 18446744074 secondes font cinq cent quatre-vingt-quatre ans ; multipliees en
			// nanosecondes elles debordent vers 290 ms, une duree qui a tout l'air d'etre
			// vraie. Sans la garde, l'appelant rejouerait tout de suite une passerelle qui
			// demandait de reculer tres fort.
			nom:     "un Retry-After qui deborde ne devient pas une attente plausible",
			entetes: map[string]string{"Retry-After": "18446744074"},
			want:    whatsapp.Debit{},
		},
		{
			nom:     "un Retry-After au dela de toute representation est ecarte",
			entetes: map[string]string{"Retry-After": "99999999999999999999999"},
			want:    whatsapp.Debit{},
		},
		{
			nom:     "X-RateLimit-Reset en secondes a attendre",
			entetes: map[string]string{"X-RateLimit-Reset": "30"},
			want:    whatsapp.Debit{Reprise: 30 * time.Second},
		},
		{
			nom:     "X-RateLimit-Reset en instant Unix",
			entetes: map[string]string{"X-RateLimit-Reset": unix(45 * time.Second)},
			want:    whatsapp.Debit{Reprise: 45 * time.Second},
		},
		{
			nom:     "X-RateLimit-Reset en millisecondes Unix",
			entetes: map[string]string{"X-RateLimit-Reset": unixMilli(90 * time.Second)},
			want:    whatsapp.Debit{Reprise: 90 * time.Second},
		},
		{
			// Retry-After est une INSTRUCTION, X-RateLimit-Reset une description de fenetre.
			// Quand les deux parlent, on suit l'instruction.
			nom: "Retry-After l emporte sur X-RateLimit-Reset",
			entetes: map[string]string{
				"Retry-After":       "10",
				"X-RateLimit-Reset": "600",
			},
			want: whatsapp.Debit{Reprise: 10 * time.Second},
		},
		{
			// Un etage inexploitable laisse repondre le suivant, au lieu d'emporter la
			// valeur parfaitement lisible posee a cote.
			nom: "un Retry-After illisible laisse X-RateLimit-Reset repondre",
			entetes: map[string]string{
				"Retry-After":       "bientot",
				"X-RateLimit-Reset": "25",
			},
			want: whatsapp.Debit{Reprise: 25 * time.Second},
		},
		{
			nom: "limite et reprise ensemble, le cas d un vrai 429",
			entetes: map[string]string{
				"X-RateLimit-Limit": "5",
				"Retry-After":       "60",
			},
			want: whatsapp.Debit{Limite: 5, Reprise: time.Minute},
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			entetes := http.Header{}
			for cle, valeur := range c.entetes {
				entetes.Set(cle, valeur)
			}

			got := lireDebit(entetes, maintenant)
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("lireDebit(%v) mal lu (-want +got):\n%s", c.entetes, diff)
			}
		})
	}
}
