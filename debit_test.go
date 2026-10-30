package whatsapp

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDebitAnnonce(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		debit Debit
		want  bool
	}{
		{nom: "valeur zero, la passerelle n a rien dit", debit: Debit{}, want: false},
		{nom: "seulement la limite", debit: Debit{Limite: 5}, want: true},
		{nom: "seulement la reprise", debit: Debit{Reprise: time.Minute}, want: true},
		{nom: "les deux", debit: Debit{Limite: 5, Reprise: time.Minute}, want: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			if got := c.debit.Annonce(); got != c.want {
				t.Errorf("Debit%+v.Annonce() = %t, want %t", c.debit, got, c.want)
			}
		})
	}
}

// TestQuotaErrorResteUneSaturation fige la propriété dont dépend tout appelant déjà écrit.
//
// Le type a été introduit APRÈS que des appelants interrogent ErrQuotaAtteint. S'il cessait
// de s'y reconnaître, leur branche « patientez » deviendrait morte sans qu'aucune compilation
// ne le signale : le refus métier se changerait en erreur technique, et l'écran afficherait un
// 500 à la place d'une phrase. C'est le genre de régression qu'un test doit tenir.
func TestQuotaErrorResteUneSaturation(t *testing.T) {
	t.Parallel()

	cause := errors.New("motif du fournisseur")

	cas := []struct {
		nom string
		err error
	}{
		{nom: "nue", err: QuotaError{}},
		{nom: "avec un debit annonce", err: QuotaError{Debit: Debit{Reprise: time.Minute}}},
		{nom: "avec une cause", err: QuotaError{Err: fmt.Errorf("%w: statut 429", ErrQuotaAtteint)}},
		{nom: "emballee par EnvoiError", err: EnvoiError{Operation: "envoi",
			Err: QuotaError{Err: fmt.Errorf("%w: statut 429", ErrQuotaAtteint)}}},
		{nom: "cause etrangere a la saturation", err: QuotaError{Err: cause}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			// SANS CONDITION, y compris sur le dernier cas : une QuotaError est une
			// saturation par construction, et non parce que sa cause se trouve mener à la
			// sentinelle. C'est ce qui rend le type utilisable dans un faux de test.
			if !errors.Is(c.err, ErrQuotaAtteint) {
				t.Errorf("errors.Is(%v, ErrQuotaAtteint) = false, want true", c.err)
			}
		})
	}

	// Et la cause reste atteignable : Is ne remplace pas la chaîne, il s'y ajoute.
	if got := errors.Is(QuotaError{Err: cause}, cause); !got {
		t.Errorf("errors.Is(QuotaError{Err: cause}, cause) = %t, want true", got)
	}
}

// TestQuotaErrorNEmpilePasSaCause tient la règle de rédaction du dépôt.
//
// Un « quota depasse: quota d envoi atteint » dans chaque journal serait la répétition que ce
// dépôt refuse. Le délai annoncé, lui, s'ajoute : c'est un fait de plus.
func TestQuotaErrorNEmpilePasSaCause(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("%w: statut 429: trop de requetes", ErrQuotaAtteint)

	cas := []struct {
		nom  string
		err  QuotaError
		want string
	}{
		{
			nom:  "sans delai annonce, la cause telle quelle",
			err:  QuotaError{Err: cause},
			want: "quota d envoi atteint: statut 429: trop de requetes",
		},
		{
			nom:  "avec un delai annonce, la cause puis le delai",
			err:  QuotaError{Debit: Debit{Reprise: 40 * time.Second}, Err: cause},
			want: "quota d envoi atteint: statut 429: trop de requetes: reprise annoncee dans 40s",
		},
		{
			nom:  "sans cause, la sentinelle sert de message",
			err:  QuotaError{},
			want: "quota d envoi atteint",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			if got := c.err.Error(); got != c.want {
				t.Errorf("QuotaError.Error() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDelaiAvantRejeu(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		err     error
		want    time.Duration
		annonce bool
	}{
		{
			nom: "erreur sans rapport",
			err: errors.New("la passerelle est indisponible"),
		},
		{
			nom: "saturation sans delai annonce",
			err: QuotaError{Err: ErrQuotaAtteint},
		},
		{
			// La forme réelle que l'appelant reçoit : le délai doit se retrouver à travers
			// les deux étages d'emballage, sinon l'aide ne sert à personne.
			nom:     "saturation emballee, telle qu elle arrive a l appelant",
			err:     EnvoiError{Operation: "envoi", Err: QuotaError{Debit: Debit{Reprise: 90 * time.Second}}},
			want:    90 * time.Second,
			annonce: true,
		},
		{
			nom: "une sentinelle nue ne porte aucun delai",
			err: ErrQuotaAtteint,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got, annonce := DelaiAvantRejeu(c.err)
			if got != c.want || annonce != c.annonce {
				t.Errorf("DelaiAvantRejeu(%v) = %v, %t, want %v, %t",
					c.err, got, annonce, c.want, c.annonce)
			}
		})
	}
}
