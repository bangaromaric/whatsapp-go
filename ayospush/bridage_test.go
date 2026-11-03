package ayospush_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	whatsapp "github.com/bangaromaric/whatsapp-go"
	"github.com/bangaromaric/whatsapp-go/ayospush"
)

// avecEntetes pose des en-têtes sur une réponse programmée.
func avecEntetes(r reponse, entetes map[string]string) reponse {
	r.entetes = entetes
	return r
}

// TestUnRefusPorteLeDelaiQueLaPasserelleAnnonce est la raison d'être de tout ce chantier.
//
// Sans lui, un appelant qui essuie un 429 ne peut que DEVINER combien de temps attendre, et
// un rejeu trop tôt est précisément ce qu'un palier d'envoi punit. Le délai doit survivre aux
// DEUX étages d'emballage que l'erreur traverse réellement, EnvoiError par-dessus QuotaError :
// un délai lisible seulement avant l'emballage ne servirait à personne.
func TestUnRefusPorteLeDelaiQueLaPasserelleAnnonce(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{avecEntetes(
			reponse{statut: http.StatusTooManyRequests, corps: `{"message":"rate limit"}`},
			map[string]string{"X-RateLimit-Limit": "5", "Retry-After": "40"})})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))

	delai, annonce := whatsapp.DelaiAvantRejeu(err)
	if !annonce || delai != 40*time.Second {
		t.Errorf("DelaiAvantRejeu(Envoyer()) = %v, %t, want %v, true", delai, annonce, 40*time.Second)
	}

	// Le débit complet reste lisible, limite comprise : c'est ce qui permet de constater
	// qu'un plafond configuré s'est écarté de celui que la passerelle applique.
	var quota whatsapp.QuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("errors.As(Envoyer(), &QuotaError{}) = false, want true, erreur = %v", err)
	}
	want := whatsapp.Debit{Limite: 5, Reprise: 40 * time.Second}
	if diff := cmp.Diff(want, quota.Debit); diff != "" {
		t.Errorf("debit annonce (-want +got):\n%s", diff)
	}
}

// TestUnRefusResteInterrogeableParLesDeuxSentinelles est le test de non-régression du portage.
//
// Les appelants écrits AVANT que le délai existe interrogent l'une ou l'autre sentinelle. Si
// l'emballage les rendait muettes, leur branche « patientez » deviendrait morte sans qu'aucune
// compilation ne le dise : le refus métier se changerait en erreur technique, et l'écran
// afficherait un 500 à la place d'une phrase en français.
func TestUnRefusResteInterrogeableParLesDeuxSentinelles(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		entetes map[string]string
	}{
		{nom: "avec un delai annonce", entetes: map[string]string{"Retry-After": "40"}},
		{nom: "sans aucun en-tete de bridage", entetes: nil},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t,
				[]reponse{loginOK("jeton-1", 3600)},
				[]reponse{avecEntetes(
					reponse{statut: http.StatusTooManyRequests, corps: `{"message":"refuse"}`},
					c.entetes)})

			client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))

			// Celle du module d'envoi : interrogeable sans connaître le fournisseur.
			if !errors.Is(err, whatsapp.ErrQuotaAtteint) {
				t.Errorf("errors.Is(Envoyer(), whatsapp.ErrQuotaAtteint) = false, want true, erreur = %v", err)
			}
			// Celle du fournisseur : la cause n'a pas été remplacée, seulement emballée.
			if !errors.Is(err, ayospush.ErrQuota) {
				t.Errorf("errors.Is(Envoyer(), ayospush.ErrQuota) = false, want true, erreur = %v", err)
			}
			// Et un refus sans en-tête n'invente aucun délai.
			_, annonce := whatsapp.DelaiAvantRejeu(err)
			if want := c.entetes != nil; annonce != want {
				t.Errorf("delai annonce = %t, want %t", annonce, want)
			}
		})
	}
}

// TestLObservateurVoitLeBridageDesLaPremiereReponseHeureuse fige ce qui fait l'intérêt du
// rappel : il parle AVANT le premier refus.
//
// Découvrir au 429 que le plafond configuré est faux, c'est le découvrir une fois qu'un
// message a déjà été refusé. Ici la passerelle annonce sa limite sur un envoi accepté, donc
// l'écart se lit dans un journal sans avoir rien coûté.
func TestLObservateurVoitLeBridageDesLaPremiereReponseHeureuse(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{avecEntetes(envoiOK("req-1"),
			map[string]string{"X-RateLimit-Limit": "5"})})

	// Le rappel est fait sur la goroutine de l'envoi. Le verrou n'est pas décoratif : sans
	// lui, le détecteur de course signalerait l'écriture, à juste titre.
	var mu sync.Mutex
	var vus []whatsapp.Debit

	client, err := ayospush.Nouveau(reglages(p),
		ayospush.AvecClientHTTP(p.srv.Client()),
		ayospush.AvecObservateur(func(d whatsapp.Debit) {
			mu.Lock()
			defer mu.Unlock()
			vus = append(vus, d)
		}))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	if _, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t)); err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []whatsapp.Debit{{Limite: 5}}
	if diff := cmp.Diff(want, vus); diff != "" {
		t.Errorf("debits observes (-want +got):\n%s", diff)
	}
}

// TestLObservateurVoitAussiLAbsenceDAnnonce fige la correction de la v0.2.1.
//
// IL FILTRAIT LE CAS VIDE, et c'était un défaut : l'absence d'en-tête devenait indiscernable
// d'une limite confortable, puisque les deux produisaient le même silence. Un appelant qui
// cherche à savoir ce que sa passerelle autorise ne pouvait pas trancher entre « elle
// m'accorde large » et « elle ne dit rien », ce qui est justement la question qu'un
// observateur sert à poser.
//
// Mesuré chez un consommateur le 3 novembre 2026 : un code parti, un observateur muet, et
// aucun moyen de savoir laquelle des deux situations on observait.
func TestLObservateurVoitAussiLAbsenceDAnnonce(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{envoiOK("req-1")})

	var mu sync.Mutex
	var vus []whatsapp.Debit

	client, err := ayospush.Nouveau(reglages(p),
		ayospush.AvecClientHTTP(p.srv.Client()),
		ayospush.AvecObservateur(func(d whatsapp.Debit) {
			mu.Lock()
			defer mu.Unlock()
			vus = append(vus, d)
		}))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	if _, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t)); err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// UN SEUL RAPPEL, PORTANT LA VALEUR ZÉRO. C'est ce qui permet à l'appelant de dire « la
	// passerelle n'annonce rien », au lieu de ne rien pouvoir dire du tout.
	want := []whatsapp.Debit{{}}
	if diff := cmp.Diff(want, vus); diff != "" {
		t.Errorf("debits observes (-want +got):\n%s", diff)
	}
	if len(vus) == 1 && vus[0].Annonce() {
		t.Error("Annonce() rend vrai sur un debit vide, want faux")
	}
}
