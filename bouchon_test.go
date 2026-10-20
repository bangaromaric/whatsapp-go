package whatsapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// journalMuet rend un journal qui n'écrit nulle part.
//
// Le bouchon journalise à chaque envoi, et la sortie des tests n'a pas à porter ces lignes :
// ce qui se vérifie ici est la référence rendue, pas la trace.
func journalMuet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// modeleMuet rend un modèle valide, pour les envois simulés.
func modeleMuet(t *testing.T) Modele {
	t.Helper()

	m, err := NouveauModele("gabarit_de_test", "123456")
	if err != nil {
		t.Fatalf("NouveauModele() = _, %v, want nil", err)
	}
	return m
}

// TestLeBouchonMarqueSesReferences fige la marque qui rend le mensonge visible.
//
// Une ligne qui porte « bouchon- » n'est jamais partie. Sans ce préfixe, il faudrait
// retrouver la valeur d'une variable d'environnement d'il y a trois semaines pour le savoir.
func TestLeBouchonMarqueSesReferences(t *testing.T) {
	t.Parallel()

	got, err := NouveauBouchon(journalMuet()).Envoyer(context.Background(), "+24166123456", modeleMuet(t))
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	if !strings.HasPrefix(got.String(), "bouchon-") {
		t.Errorf("Reference = %q, want le prefixe bouchon-", got)
	}
	if got.Absente() {
		t.Error("Reference absente, want une reference utilisable")
	}
}

// TestLaReferenceDuBouchonPasseraitLaValidation garde un invariant que le code contourne.
//
// Le bouchon construit Reference par son champ et non par [NouvelleReference], comme tout
// code du paquet a le droit de le faire. Ce test est ce qui empêche ce raccourci de produire
// une valeur que le constructeur refuserait, donc une référence que l'appelant ne pourrait
// pas reconstruire depuis sa base.
func TestLaReferenceDuBouchonPasseraitLaValidation(t *testing.T) {
	t.Parallel()

	got, err := NouveauBouchon(journalMuet()).Envoyer(context.Background(), "+24166123456", modeleMuet(t))
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	if _, err := NouvelleReference(got.String()); err != nil {
		t.Errorf("NouvelleReference(%q) = _, %v, want nil", got, err)
	}
}

// TestDeuxEnvoisSimulesNeSeConfondentPas couvre la seule propriété utile de l'identifiant.
//
// Deux envois simulés qui porteraient la même référence rendraient la trace de développement
// inutilisable : on ne saurait plus lequel des deux messages une ligne désigne.
func TestDeuxEnvoisSimulesNeSeConfondentPas(t *testing.T) {
	t.Parallel()

	bouchon := NouveauBouchon(journalMuet())
	m := modeleMuet(t)

	premier, err := bouchon.Envoyer(context.Background(), "+24166123456", m)
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}
	second, err := bouchon.Envoyer(context.Background(), "+24166123456", m)
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	if premier.String() == second.String() {
		t.Errorf("deux envois rendent tous deux %q, want deux references distinctes", premier)
	}
}

// TestLeBouchonRefuseUnModeleVide vérifie que le bouchon ne mente pas plus qu'il ne faut.
//
// Il rend toujours un succès, c'est son principe, mais pas sur une entrée que la passerelle
// réelle refuserait : un modèle vide doit casser au poste de travail et non en production.
func TestLeBouchonRefuseUnModeleVide(t *testing.T) {
	t.Parallel()

	got, err := NouveauBouchon(journalMuet()).Envoyer(context.Background(), "+24166123456", Modele{})
	if err == nil {
		t.Fatalf("Envoyer(modele vide) = %q, nil, want une erreur", got)
	}

	if _, ok := errors.AsType[EnvoiError](err); !ok {
		t.Errorf("Envoyer(modele vide) = _, %v, want une EnvoiError", err)
	}
	if !got.Absente() {
		t.Errorf("Envoyer(modele vide) = %q, want la reference absente", got)
	}
}

// TestLaValeurZeroDuBouchonJournaliseSurLeDefaut couvre ce que la documentation promet.
func TestLaValeurZeroDuBouchonJournaliseSurLeDefaut(t *testing.T) {
	t.Parallel()

	if got := (Bouchon{}).logger(); got != slog.Default() {
		t.Errorf("Bouchon{}.logger() = %p, want slog.Default() %p", got, slog.Default())
	}
}

// TestMasquerNumero vérifie qu'un journal ne peut pas reconstituer un numéro.
func TestMasquerNumero(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		entree string
		want   string
	}{
		{nom: "numero gabonais", entree: "+24166123456", want: "+241****56"},
		{nom: "sans le plus", entree: "24166123456", want: "2416****56"},
		{nom: "trop court pour masquer", entree: "12345", want: "****"},
		{nom: "vide", entree: "", want: "****"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			if got := MasquerNumero(c.entree); got != c.want {
				t.Errorf("MasquerNumero(%q) = %q, want %q", c.entree, got, c.want)
			}
		})
	}
}
