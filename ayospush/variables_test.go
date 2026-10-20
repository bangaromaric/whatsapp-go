package ayospush

import "testing"

// TestVariablesJSON fige la sérialisation du champ le plus piégeux du contrat.
//
// Le cas à onze variables est le seul qui démasque une implémentation par map, et c'est
// pourquoi il est là : encoding/json ordonne les clés d'une map lexicographiquement, donc
// « 10 » et « 11 » se rangent entre « 1 » et « 2 ». Un tel code passerait tous les autres cas
// de ce tableau, et enverrait des messages faux dès le jour où un template gagne une dixième
// variable, sans qu'aucune erreur ne soit levée nulle part.
func TestVariablesJSON(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		variables []string
		want      string
	}{
		{
			nom:       "aucune variable",
			variables: nil,
			want:      `{}`,
		},
		{
			nom:       "une variable",
			variables: []string{"123456"},
			want:      `{"1":"123456"}`,
		},
		{
			nom:       "deux variables dans l ordre du modele",
			variables: []string{"C-000123", "https://kaye.banga.ga/s/abc"},
			want:      `{"1":"C-000123","2":"https://kaye.banga.ga/s/abc"}`,
		},
		{
			nom:       "onze variables, le cas qui demasque une map",
			variables: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"},
			want: `{"1":"a","2":"b","3":"c","4":"d","5":"e","6":"f","7":"g","8":"h",` +
				`"9":"i","10":"j","11":"k"}`,
		},
		{
			nom:       "accents conserves",
			variables: []string{"Boutique Élégance"},
			want:      `{"1":"Boutique Élégance"}`,
		},
		{
			nom:       "guillemet echappe",
			variables: []string{`Chez "Paul"`},
			want:      `{"1":"Chez \"Paul\""}`,
		},
		{
			nom:       "barre oblique inverse echappee",
			variables: []string{`a\b`},
			want:      `{"1":"a\\b"}`,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got, err := variablesJSON(c.variables)
			if err != nil {
				t.Fatalf("variablesJSON(%q) = _, %v, want nil", c.variables, err)
			}
			if got != c.want {
				t.Errorf("variablesJSON(%q) = %s, want %s", c.variables, got, c.want)
			}
		})
	}
}
