package whatsapp

import (
	"net/url"
	"strings"
	"testing"
)

// TestNumero couvre la seule transformation que ce module s'autorise.
func TestNumero(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		e164    string
		want    string
		wantErr bool
	}{
		{nom: "mobile moov", e164: "+24162000000", want: "24162000000"},
		{
			// Un E.164 gabonais ne porte PAS le zéro national : il fait +241 suivi de huit
			// chiffres. C'est ce que rend une analyse E.164 correcte, et ce que wa.me attend. Une
			// version antérieure gardait ce zéro et composait wa.me/241066123456, qui porte
			// un chiffre de trop et ne désigne personne : les messages partaient dans le vide.
			nom: "mobile airtel", e164: "+24177123456", want: "24177123456",
		},
		{nom: "fixe", e164: "+24111760000", want: "24111760000"},
		{nom: "espaces autour", e164: "  +24166123456  ", want: "24166123456"},
		{nom: "numero absent", e164: "", wantErr: true},
		{nom: "plus seul", e164: "+", wantErr: true},
		{nom: "caracteres parasites", e164: "+241 066 12 34 56", wantErr: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got, err := Numero(c.e164)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Numero(%q) = %q, nil, want une erreur", c.e164, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Numero(%q) = _, %v, want nil", c.e164, err)
			}
			if got != c.want {
				t.Errorf("Numero(%q) = %q, want %q", c.e164, got, c.want)
			}
		})
	}
}

// TestLesEspacesSontEncodesSansPlus fige le choix d'encodage.
//
// Le plus ne vaut espace que sous les règles des formulaires web. WhatsApp ouvre ce lien
// depuis une application mobile, et rien ne garantit qu'elle les applique : un message
// affiché « Bonjour+Mireille » détruirait le ton de n'importe quel message soigné.
func TestLesEspacesSontEncodesSansPlus(t *testing.T) {
	t.Parallel()

	lien := Lien("24166123456", "Bonjour Mireille, votre solde est de 45 000 XAF")

	requete := strings.TrimPrefix(lien, "https://wa.me/24166123456?text=")
	if strings.Contains(requete, "+") {
		t.Errorf("le lien encode un espace en plus: %s", lien)
	}
	if !strings.Contains(requete, "%20") {
		t.Errorf("le lien n encode aucun espace en %%20: %s", lien)
	}
}

// TestUnPlusLitteralSurvit vérifie que le remplacement ne corrompt pas un message qui
// contient réellement un plus, par exemple un numéro écrit en entier.
func TestUnPlusLitteralSurvit(t *testing.T) {
	t.Parallel()

	const message = "Rappel de la boutique, joignable au +24106000111"
	analyse, err := url.Parse(Lien("24166123456", message))
	if err != nil {
		t.Fatalf("Lien illisible: %v", err)
	}
	if got := analyse.Query().Get("text"); got != message {
		t.Errorf("texte transporte = %q, want %q", got, message)
	}
}
