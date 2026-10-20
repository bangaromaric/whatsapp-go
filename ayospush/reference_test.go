package ayospush

import (
	"encoding/json"
	"testing"
)

// L'identifiant de message est la SEULE clé qui permette de retrouver un message chez le
// fournisseur : support, réconciliation, et accusé de livraison le jour où un webhook
// existera. D'où deux exigences opposées que ces tests tiennent ensemble.
//
// Tolérant sur le TYPE : le guide d'intégration le montre en chaîne, la passerelle rend un
// entier. Un champ typé faisait échouer le décodage de toute la réponse, donc l'envoi était
// rapporté en échec alors que le message était parti. Le destinataire recevait son message et
// le serveur disait non.
//
// Strict sur l'ABSENCE : un 2xx sans identifiant reste un échec. Accepter l'envoi sans lui
// écrirait qu'un message est parti sans aucun moyen de le prouver ni de le retrouver.

func TestReferenceDe(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		brut  string
		want  string
		lisib bool
	}{
		{
			// La forme RÉELLE, relevée le 8 octobre 2026.
			nom: "entier, la forme reelle", brut: `9168`, want: "9168", lisib: true,
		},
		{
			nom: "chaine, la forme du guide", brut: `"req-9"`, want: "req-9", lisib: true,
		},
		{
			// Le jeton brut est déjà la forme textuelle : pas de passage par un flottant,
			// qui écrirait « 9168.000000 » ou perdrait des chiffres sur un identifiant
			// plus long que la précision d'un float64.
			nom: "entier long", brut: `9007199254740993`, want: "9007199254740993", lisib: true,
		},
		{
			nom: "chaine avec un echappement", brut: `"req-9"`, want: "req-9", lisib: true,
		},
		{nom: "absent", brut: ``, lisib: false},
		{nom: "nul", brut: `null`, lisib: false},
		{nom: "chaine vide", brut: `""`, lisib: false},
		{nom: "objet", brut: `{"id":9168}`, lisib: false},
		{nom: "tableau", brut: `[9168]`, lisib: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got, lisible := referenceDe(json.RawMessage(c.brut))
			if lisible != c.lisib {
				t.Fatalf("referenceDe(%s) lisible = %v, want %v", c.brut, lisible, c.lisib)
			}
			if lisible && got != c.want {
				t.Errorf("referenceDe(%s) = %q, want %q", c.brut, got, c.want)
			}
		})
	}
}

// TestLaChargeUtileDEnvoiReelleEstLue fige la réponse exacte de la passerelle.
//
// Relevée le 8 octobre 2026. Elle porte deux écarts au guide : `request_id` est un ENTIER, et
// trois champs s'ajoutent, `recipient`, `template` et `message`, dont on n'a aucun usage parce
// qu'ils répètent ce qu'on vient d'envoyer.
//
// Le test passe par le DÉCODAGE et pas seulement par referenceDe, parce que c'est là qu'était
// le défaut : la réponse entière refusait de se décoder avant qu'on lise quoi que ce soit.
func TestLaChargeUtileDEnvoiReelleEstLue(t *testing.T) {
	t.Parallel()

	const reelle = `{"success":true,"data":{"request_id":9168,"status":"queued",` +
		`"recipient":"24166127676","template":"authkaye_biz1_1008135823"},` +
		`"message":"Message queued for delivery"}`

	var lue reponseEnvoi
	if err := json.Unmarshal([]byte(reelle), &lue); err != nil {
		t.Fatalf("decoder la reponse reelle = %v, want nil", err)
	}

	got, lisible := referenceDe(lue.Data.RequestID)
	if !lisible {
		t.Fatal("referenceDe() illisible, want la reference du message")
	}
	if want := "9168"; got != want {
		t.Errorf("referenceDe() = %q, want %q", got, want)
	}
}
