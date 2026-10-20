package ayospush

import (
	"encoding/json"
	"testing"
	"time"
)

// Le guide d'intégration documente `expires_in_seconds` ; la passerelle réelle, mesurée le
// 8 octobre 2026, rend `expires_at`. Ces tests figent la lecture des deux, et surtout le
// comportement quand on n'y comprend rien : retomber sur le repli d'une heure plutôt que de
// garder une durée absurde.
//
// Pourquoi cela compte : un instant d'expiration lu comme des secondes alors qu'il est en
// millisecondes donne un jeton réputé valable trente mille ans, donc jamais renouvelé, donc un
// 401 à chaque envoi dès la deuxième heure. L'inverse donne une reconnexion à chaque envoi.

// reponse décode une réponse de connexion écrite à la main.
func reponse(t *testing.T, corps string) reponseLogin {
	t.Helper()

	var lue reponseLogin
	if err := json.Unmarshal([]byte(corps), &lue); err != nil {
		t.Fatalf("decoder %s = %v, want nil", corps, err)
	}
	return lue
}

func TestLaDureeDeVieDuJeton(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC)

	cas := []struct {
		nom   string
		corps string
		want  time.Duration
	}{
		{
			nom:   "duree explicite, la forme du guide",
			corps: `{"data":{"token":"t","expires_in_seconds":3600}}`,
			want:  time.Hour,
		},
		{
			// La forme REELLE, relevee le 8 octobre 2026. Un flottant, la ou le guide
			// annonce un entier, et c'est ce qui a casse la connexion : un *int64
			// faisait echouer le decodage de toute la structure, donc le jeton n'etait
			// jamais lu.
			nom:   "duree en flottant, la forme reelle",
			corps: `{"data":{"token":"t","expires_in_seconds":3599.995884}}`,
			want:  3599995884000,
		},
		{
			nom:   "instant en secondes unix, la forme reelle",
			corps: `{"data":{"token":"t","expires_at":1791486000}}`,
			want:  time.Hour,
		},
		{
			nom:   "instant en millisecondes",
			corps: `{"data":{"token":"t","expires_at":1791486000000}}`,
			want:  time.Hour,
		},
		{
			nom:   "instant en chaine RFC 3339",
			corps: `{"data":{"token":"t","expires_at":"2026-10-08T19:00:00Z"}}`,
			want:  time.Hour,
		},
		{
			nom:   "instant en chaine sans fuseau",
			corps: `{"data":{"token":"t","expires_at":"2026-10-08 19:00:00"}}`,
			want:  time.Hour,
		},
		{
			// La durée explicite l'emporte : c'est la source la plus directe, et la
			// seule qui ne dépende pas de l'accord des horloges.
			nom:   "la duree explicite passe devant l instant",
			corps: `{"data":{"token":"t","expires_in_seconds":1800,"expires_at":1791486000}}`,
			want:  30 * time.Minute,
		},
		{
			nom:   "rien d annonce",
			corps: `{"data":{"token":"t"}}`,
			want:  0,
		},
		{
			nom:   "duree nulle",
			corps: `{"data":{"token":"t","expires_in_seconds":0}}`,
			want:  0,
		},
		{
			nom:   "duree negative",
			corps: `{"data":{"token":"t","expires_in_seconds":-10}}`,
			want:  0,
		},
		{
			// Un instant déjà passé ne vaut pas mieux que rien : l'appelant prendra son
			// repli plutôt que de poser une expiration dans le passé.
			nom:   "instant deja passe",
			corps: `{"data":{"token":"t","expires_at":1735689600}}`,
			want:  0,
		},
		{
			nom:   "instant absurde, lu comme un mauvais format",
			corps: `{"data":{"token":"t","expires_at":99999999999}}`,
			want:  0,
		},
		{
			nom:   "duree au-dela du plafond",
			corps: `{"data":{"token":"t","expires_in_seconds":172800}}`,
			want:  0,
		},
		{
			nom:   "instant illisible",
			corps: `{"data":{"token":"t","expires_at":"bientot"}}`,
			want:  0,
		},
		{
			nom:   "instant nul",
			corps: `{"data":{"token":"t","expires_at":null}}`,
			want:  0,
		},
		{
			nom:   "instant a zero",
			corps: `{"data":{"token":"t","expires_at":0}}`,
			want:  0,
		},
		{
			// Une forme inattendue ne doit pas faire échouer la connexion : la durée de
			// vie n'est qu'une optimisation du cache, et le repli d'une heure suffit.
			nom:   "instant dans une forme inconnue",
			corps: `{"data":{"token":"t","expires_at":{"quand":"plus tard"}}}`,
			want:  0,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got := reponse(t, c.corps).dureeDeVie(maintenant)
			if got != c.want {
				t.Errorf("dureeDeVie() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestLaChargeUtileReelleEstLue fige ce que la passerelle rend vraiment.
//
// Relevée le 8 octobre 2026, token retiré. Elle porte trois surprises par rapport au guide :
// `expires_in_seconds` est un FLOTTANT, `expires_at` existe en plus et porte un décalage de
// fuseau, et un tableau `scopes` apparaît, dont on n'a aucun usage.
//
// Ce test passe par le DÉCODAGE, et pas seulement par dureeDeVie, parce que c'est là qu'était
// le défaut : la fonction d'analyse était juste, et la structure refusait le flottant avant
// qu'on l'appelle. Tester la fonction sans tester le chemin laissait exactement ce trou.
func TestLaChargeUtileReelleEstLue(t *testing.T) {
	t.Parallel()

	const reelle = `{"success":true,"data":{` +
		`"token":"ayospush_tmlpx2_1_xxx",` +
		`"expires_at":"2026-10-08T20:37:05+01:00",` +
		`"expires_in_seconds":3599.995884,` +
		`"scopes":["messages.send","messages.read","templates.send","templates.read",` +
		`"templates.write","logs.read","account.read","config.read"]}}`

	lue := reponse(t, reelle)

	if lue.Data.Token != "ayospush_tmlpx2_1_xxx" {
		t.Errorf("Token = %q, want la valeur de la reponse", lue.Data.Token)
	}

	// La durée annoncée est utilisée telle quelle, et non remplacée par le repli : c'est
	// tout l'intérêt de la lire.
	got := lue.dureeDeVie(time.Date(2026, 10, 8, 18, 37, 5, 0, time.UTC))
	if got < 59*time.Minute || got > time.Hour {
		t.Errorf("dureeDeVie() = %v, want environ une heure", got)
	}
}

// TestUneFormeInconnueNEmpechePasLaConnexion fige la propriété la plus importante du lot.
//
// Le jeton est ce dont on a besoin ; sa durée de vie est un confort. Faire échouer la
// connexion parce qu'on n'a pas su lire une date rendrait le canal entier tributaire d'un
// champ secondaire, et un changement cosmétique chez le fournisseur couperait
// l'authentification du produit.
func TestUneFormeInconnueNEmpechePasLaConnexion(t *testing.T) {
	t.Parallel()

	lue := reponse(t, `{"data":{"token":"jeton-1","expires_at":["pas","une","date"]}}`)

	if lue.Data.Token != "jeton-1" {
		t.Errorf("Token = %q, want %q", lue.Data.Token, "jeton-1")
	}
	if got := lue.dureeDeVie(time.Now()); got != 0 {
		t.Errorf("dureeDeVie() = %v, want 0 pour que l appelant prenne son repli", got)
	}
}
