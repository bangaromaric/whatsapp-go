package ayospush_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	whatsapp "github.com/bangaromaric/whatsapp-go"
	"github.com/bangaromaric/whatsapp-go/ayospush"
)

// Les tests parlent au niveau HTTP, contre un serveur simulé, et jamais à un simulacre du
// client HTTP. La distinction n'est pas théorique : simuler le client vérifierait que
// l'adaptateur appelle bien le simulacre, ce qui est toujours vrai et n'apprend rien. Ce
// qu'on veut éprouver est la REQUÊTE RÉELLEMENT ÉMISE, parce que c'est elle qui échoue en
// production : un champ manquant, un plus resté dans le numéro, des clés de variables dans le
// mauvais ordre, un type de contenu écrit à la main.
//
// Un SEUL serveur sert les deux points d'entrée, et c'est le montage qui rend ces tests
// crédibles : la connexion et l'envoi sont interceptés dans l'ordre, par le même témoin, donc
// « combien de connexions pour deux envois » devient une question à laquelle le test répond.

const (
	cheminLogin = "/api/v1/auth/login"
	cheminEnvoi = "/api/v1/messages/template"
)

// reponse est une réponse programmée du faux fournisseur.
type reponse struct {
	statut int
	corps  string
}

// recue est ce que le faux fournisseur a vu arriver.
//
// Les champs sont exportés pour que cmp.Diff les compare : une comparaison champ par champ
// dirait « le type de contenu diffère » sans montrer le reste de la requête, là où un diff
// de la structure entière met l'erreur en contexte.
type recue struct {
	Chemin       string
	Autorisation string
	TypeContenu  string
	Champs       map[string]string
}

// passerelle est un faux AyosPush qui enregistre tout et répond ce qu'on lui dit.
type passerelle struct {
	srv *httptest.Server

	// mu garde les trois tranches. Le gestionnaire HTTP tourne sur une autre goroutine
	// que le test, et sans ce verrou le détecteur de course le signale, à juste titre.
	mu     sync.Mutex
	recues []recue
	logins []reponse
	envois []reponse
	iLogin int
	iEnvoi int
}

// nouvellePasserelle démarre le faux fournisseur.
//
// Une liste de réponses se consomme dans l'ordre ; passé la dernière, c'est la dernière qui
// se répète. Cela évite d'écrire dix réponses identiques pour un test qui envoie dix fois.
func nouvellePasserelle(t *testing.T, logins, envois []reponse) *passerelle {
	t.Helper()

	p := &passerelle{logins: logins, envois: envois}

	mux := http.NewServeMux()
	mux.HandleFunc(cheminLogin, p.servirLogin)
	mux.HandleFunc(cheminEnvoi, p.servirEnvoi)

	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *passerelle) servirLogin(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.recues = append(p.recues, recue{Chemin: r.URL.Path, TypeContenu: r.Header.Get("Content-Type")})
	r2 := suivante(p.logins, &p.iLogin)
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r2.statut)
	_, _ = w.Write([]byte(r2.corps))
}

func (p *passerelle) servirEnvoi(w http.ResponseWriter, r *http.Request) {
	vue := recue{
		Chemin:       r.URL.Path,
		Autorisation: r.Header.Get("Authorization"),
		TypeContenu:  typeMultipart(r.Header.Get("Content-Type")),
		Champs:       map[string]string{},
	}
	// Le corps est lu AVANT de prendre le verrou : analyser un multipart sous un verrou
	// que le test prend aussi ferait un point de contention sans aucune raison.
	if err := r.ParseMultipartForm(1 << 20); err == nil {
		for cle, valeurs := range r.MultipartForm.Value {
			if len(valeurs) > 0 {
				vue.Champs[cle] = valeurs[0]
			}
		}
	}

	p.mu.Lock()
	p.recues = append(p.recues, vue)
	r2 := suivante(p.envois, &p.iEnvoi)
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r2.statut)
	_, _ = w.Write([]byte(r2.corps))
}

// suivante rend la réponse programmée du tour, la dernière se répétant. Verrou tenu.
func suivante(programme []reponse, i *int) reponse {
	if len(programme) == 0 {
		return reponse{statut: http.StatusInternalServerError, corps: `{}`}
	}
	if *i >= len(programme) {
		return programme[len(programme)-1]
	}
	r := programme[*i]
	*i++
	return r
}

// typeMultipart ne garde que le type, sans la limite de séparation.
//
// La limite est tirée au hasard à chaque envoi : la comparer figerait un aléa. Ce qui se
// vérifie est qu'une limite EXISTE, c'est à dire que le type de contenu vient bien de
// l'écrivain multipart et non d'une chaîne écrite à la main.
func typeMultipart(entete string) string {
	if !strings.Contains(entete, "boundary=") {
		return entete + " (SANS LIMITE DE SEPARATION)"
	}
	return strings.TrimSpace(strings.Split(entete, ";")[0])
}

// requetes rend ce que le faux fournisseur a vu, dans l'ordre.
func (p *passerelle) requetes() []recue {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recue(nil), p.recues...)
}

// chemins rend la suite des points d'entrée appelés, ce qui est souvent tout ce qu'un test
// veut vérifier.
func (p *passerelle) chemins() []string {
	var suite []string
	for _, r := range p.requetes() {
		suite = append(suite, r.Chemin)
	}
	return suite
}

// horloge est une source de temps que le test avance à la main.
//
// Aucun time.Sleep dans ces tests : les règles du dépôt l'interdisent comme moyen de
// synchronisation, et attendre une heure pour éprouver l'expiration d'un jeton n'est de toute
// façon pas un test.
type horloge struct {
	mu      sync.Mutex
	instant time.Time
}

func nouvelleHorloge() *horloge {
	return &horloge{instant: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
}

func (h *horloge) maintenant() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.instant
}

func (h *horloge) avancer(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instant = h.instant.Add(d)
}

// Réponses courantes, écrites une fois.
func loginOK(jeton string, ttl int) reponse {
	return reponse{statut: http.StatusOK, corps: `{"success":true,"data":{"token":"` + jeton +
		`","expires_in_seconds":` + itoa(ttl) + `}}`}
}

func envoiOK(reference string) reponse {
	return reponse{statut: http.StatusAccepted,
		corps: `{"success":true,"data":{"request_id":"` + reference + `","status":"queued"}}`}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var chiffres []byte
	for n > 0 {
		chiffres = append([]byte{byte('0' + n%10)}, chiffres...)
		n /= 10
	}
	return string(chiffres)
}

// reglages rend des réglages valides pointant le faux fournisseur.
func reglages(p *passerelle) ayospush.Reglages {
	return ayospush.Reglages{
		BaseURL:    p.srv.URL,
		CleAPI:     "cle-de-test",
		SecretAPI:  "secret-de-test",
		Expediteur: "PNID-1",
	}
}

// modeleDeTest est le modèle du code d'authentification : une variable, le code.
func modeleDeTest(t *testing.T) whatsapp.Modele {
	t.Helper()

	m, err := whatsapp.NouveauModele("kaye_code_auth", "123456")
	if err != nil {
		t.Fatalf("NouveauModele() = _, %v, want nil", err)
	}
	return m
}

// TestConnexionPuisEnvoiNominal est le scénario qui fige la requête réellement émise.
//
// Il vérifie les quatre pièges du portage d'un coup : le numéro sans le plus, les quatre
// champs multipart présents et nommés comme la passerelle les attend, la map de variables
// indexée depuis un et sérialisée en CHAÎNE, et le jeton porté en en-tête.
func TestConnexionPuisEnvoiNominal(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{envoiOK("req-9")})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	reference, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}

	if got, want := reference.String(), "req-9"; got != want {
		t.Errorf("Envoyer() = %q, want %q", got, want)
	}

	got := p.requetes()
	want := []recue{
		{Chemin: cheminLogin, TypeContenu: "application/json"},
		{
			Chemin:       cheminEnvoi,
			Autorisation: "Bearer jeton-1",
			TypeContenu:  "multipart/form-data",
			Champs: map[string]string{
				"phone_number_id": "PNID-1",
				// Sans le plus, et sans séparateur : la passerelle veut des chiffres.
				"recipient_number": "24166123456",
				"template_name":    "kaye_code_auth",
				// Une chaîne contenant du JSON, et non un objet JSON.
				"variables": `{"1":"123456"}`,
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("requetes recues, -want +got:\n%s", diff)
	}
}

// TestLesChargesUtilesReellesPassentDeBoutEnBout rejoue les reponses exactes de la passerelle.
//
// Relevees le 8 octobre 2026, jeton et identifiants retires. Elles portent trois ecarts au
// guide d'integration, et chacun des trois avait casse l'envoi : expires_in_seconds est un
// FLOTTANT, expires_at existe en plus, et request_id est un ENTIER.
//
// Ce test vaut plus que les trois tests unitaires de format reunis : il prouve que le chemin
// complet, connexion puis envoi puis lecture de la reference, tient contre ce que la
// passerelle rend vraiment, et pas contre ce qu'un document annonce.
func TestLesChargesUtilesReellesPassentDeBoutEnBout(t *testing.T) {
	t.Parallel()

	const loginReel = `{"success":true,"data":{"token":"ayospush_tmlpx2_1_xxx",` +
		`"expires_at":"2026-10-08T20:37:05+01:00","expires_in_seconds":3599.995884,` +
		`"scopes":["messages.send","templates.send"]}}`

	const envoiReel = `{"success":true,"data":{"request_id":9168,"status":"queued",` +
		`"recipient":"24166127676","template":"authkaye_biz1_1008135823"},` +
		`"message":"Message queued for delivery"}`

	p := nouvellePasserelle(t,
		[]reponse{{statut: http.StatusOK, corps: loginReel}},
		[]reponse{{statut: http.StatusOK, corps: envoiReel}})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	reference, err := client.Envoyer(context.Background(), "+24166127676", modeleDeTest(t))
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}
	// L'entier devient sa forme textuelle, sans passer par un flottant.
	if got, want := reference.String(), "9168"; got != want {
		t.Errorf("Envoyer() = %q, want %q", got, want)
	}

	// La duree annoncee est utilisee : un second envoi ne doit pas repayer de connexion.
	if _, err := client.Envoyer(context.Background(), "+24166127676", modeleDeTest(t)); err != nil {
		t.Fatalf("second Envoyer() = _, %v, want nil", err)
	}
	gotChemins := p.chemins()
	wantChemins := []string{cheminLogin, cheminEnvoi, cheminEnvoi}
	if diff := cmp.Diff(wantChemins, gotChemins); diff != "" {
		t.Errorf("suite des appels, -want +got:\n%s", diff)
	}
}

// TestRenouvelleLeJetonSurUn401 couvre le rejeu unique.
//
// Quatre requêtes dans l'ordre, et le second envoi porte le NOUVEAU jeton : c'est la preuve
// que le jeton périmé n'a pas été réutilisé depuis le cache.
func TestRenouvelleLeJetonSurUn401(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-vieux", 3600), loginOK("jeton-neuf", 3600)},
		[]reponse{{statut: http.StatusUnauthorized, corps: `{"message":"token expired"}`}, envoiOK("req-10")})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	reference, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
	if err != nil {
		t.Fatalf("Envoyer() = _, %v, want nil", err)
	}
	if got, want := reference.String(), "req-10"; got != want {
		t.Errorf("Envoyer() = %q, want %q", got, want)
	}

	gotChemins := p.chemins()
	wantChemins := []string{cheminLogin, cheminEnvoi, cheminLogin, cheminEnvoi}
	if diff := cmp.Diff(wantChemins, gotChemins); diff != "" {
		t.Errorf("suite des appels, -want +got:\n%s", diff)
	}

	requetes := p.requetes()
	if got, want := requetes[3].Autorisation, "Bearer jeton-neuf"; got != want {
		t.Errorf("autorisation du second envoi = %q, want %q", got, want)
	}

	// Le corps du rejeu doit être intact. Renvoyer le même io.Reader enverrait un corps
	// vide, et la passerelle rendrait un 400 que rien n'expliquerait.
	if diff := cmp.Diff(requetes[1].Champs, requetes[3].Champs); diff != "" {
		t.Errorf("le corps du rejeu differe du premier envoi, -premier +rejeu:\n%s", diff)
	}
}

// TestNeRejoueQuUneSeuleFois fige la borne du rejeu.
//
// Au deuxième refus consécutif, le problème n'est plus le jeton mais les droits. Insister
// masquerait une faute de configuration derrière une rafale de requêtes.
func TestNeRejoueQuUneSeuleFois(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600), loginOK("jeton-2", 3600)},
		[]reponse{{statut: http.StatusUnauthorized, corps: `{}`}})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
	if !errors.Is(err, ayospush.ErrAuthentification) {
		t.Errorf("Envoyer() = _, %v, want une erreur qui est ErrAuthentification", err)
	}

	gotChemins := p.chemins()
	wantChemins := []string{cheminLogin, cheminEnvoi, cheminLogin, cheminEnvoi}
	if diff := cmp.Diff(wantChemins, gotChemins); diff != "" {
		t.Errorf("suite des appels, -want +got:\n%s", diff)
	}
}

// TestConfigurationIncompleteNEmetAucuneRequete est le test dont la seconde assertion compte.
//
// Qu'une configuration incomplète échoue est attendu. Ce qui importe est qu'elle échoue SANS
// APPELER le fournisseur : se connecter pour découvrir qu'une clé est vide enverrait des
// identifiants vides sur le réseau, et le journal d'AyosPush en garderait la trace.
func TestConfigurationIncompleteNEmetAucuneRequete(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		abimer func(*ayospush.Reglages)
	}{
		{nom: "base-url absente", abimer: func(r *ayospush.Reglages) { r.BaseURL = "" }},
		{nom: "api-key absente", abimer: func(r *ayospush.Reglages) { r.CleAPI = "" }},
		{nom: "api-secret absent", abimer: func(r *ayospush.Reglages) { r.SecretAPI = "" }},
		{nom: "sender-id absent", abimer: func(r *ayospush.Reglages) { r.Expediteur = "" }},
		{nom: "sender-id en blancs", abimer: func(r *ayospush.Reglages) { r.Expediteur = "   " }},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t, []reponse{loginOK("jeton-1", 3600)}, []reponse{envoiOK("req-1")})
			r := reglages(p)
			c.abimer(&r)

			client, err := ayospush.Nouveau(r, ayospush.AvecClientHTTP(p.srv.Client()))
			if !errors.Is(err, ayospush.ErrConfiguration) {
				t.Errorf("Nouveau() = %v, %v, want une erreur qui est ErrConfiguration", client, err)
			}
			if got := p.chemins(); len(got) != 0 {
				t.Errorf("requetes emises = %v, want aucune", got)
			}
		})
	}
}

// TestUn2xxSansIdentifiantEstUnEchec fige la règle la moins intuitive du contrat.
//
// Un 202 veut dire « accepté pour émission », pas « parti ». Sans identifiant de corrélation,
// le message est intraçable chez le fournisseur : le traiter comme un succès écrirait en base
// qu'un code est parti sans aucun moyen de le prouver ni de le retrouver.
func TestUn2xxSansIdentifiantEstUnEchec(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "data absente", corps: `{"success":true}`},
		{nom: "request_id absent", corps: `{"success":true,"data":{"status":"queued"}}`},
		{nom: "request_id vide", corps: `{"success":true,"data":{"request_id":""}}`},
		{nom: "corps vide", corps: ``},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t,
				[]reponse{loginOK("jeton-1", 3600)},
				[]reponse{{statut: http.StatusAccepted, corps: c.corps}})

			client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
			if !errors.Is(err, ayospush.ErrSansReference) {
				t.Errorf("Envoyer() = _, %v, want une erreur qui est ErrSansReference", err)
			}
			// Et surtout : pas de rejeu. Une réponse inexploitable n'est pas un jeton
			// périmé.
			if got, want := len(p.chemins()), 2; got != want {
				t.Errorf("nombre de requetes = %d, want %d", got, want)
			}
		})
	}
}

// TestUneErreurDeContratNeSeRejoueJamais fige la distinction qui évite une tempête.
//
// Un 400 ou un 422 dit « ton template est inconnu », « ton arité est fausse », « ton numéro
// est invalide ». Rejouer transformerait une faute de configuration en rafale de requêtes
// facturées, et le défaut d'origine resterait.
func TestUneErreurDeContratNeSeRejoueJamais(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		statut int
		want   error
	}{
		{nom: "400 requete invalide", statut: http.StatusBadRequest, want: ayospush.ErrRequeteRefusee},
		{nom: "422 entite non traitable", statut: http.StatusUnprocessableEntity, want: ayospush.ErrRequeteRefusee},
		{nom: "404 point d entree inconnu", statut: http.StatusNotFound, want: ayospush.ErrRequeteRefusee},
		{nom: "429 quota depasse", statut: http.StatusTooManyRequests, want: ayospush.ErrQuota},
		{nom: "500 panne du fournisseur", statut: http.StatusInternalServerError, want: ayospush.ErrIndisponible},
		{nom: "503 indisponible", statut: http.StatusServiceUnavailable, want: ayospush.ErrIndisponible},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t,
				[]reponse{loginOK("jeton-1", 3600)},
				[]reponse{{statut: c.statut, corps: `{"message":"refuse"}`}})

			client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
			if !errors.Is(err, c.want) {
				t.Errorf("Envoyer() sur %d = _, %v, want une erreur qui est %v", c.statut, err, c.want)
			}
			// La saturation est le seul motif qu'un appelant doit pouvoir reconnaître sans
			// connaître ce fournisseur : lui seul emballe la sentinelle du module d'envoi,
			// et c'est ce qui permet à l'identité d'en tirer un refus métier. Les autres ne
			// la portent pas, sinon une panne de transport se lirait comme un palier
			// atteint.
			voulu := c.want == ayospush.ErrQuota
			if got := errors.Is(err, whatsapp.ErrQuotaAtteint); got != voulu {
				t.Errorf("errors.Is(Envoyer() sur %d, ErrQuotaAtteint) = %t, want %t",
					c.statut, got, voulu)
			}
			if got, want := len(p.chemins()), 2; got != want {
				t.Errorf("nombre de requetes = %d, want %d: aucun rejeu attendu", got, want)
			}
		})
	}
}

// TestLeJetonEstMisEnCache vérifie qu'un second envoi ne repaie pas une connexion.
func TestLeJetonEstMisEnCache(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{envoiOK("req-1"), envoiOK("req-2")})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	for i := range 2 {
		if _, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t)); err != nil {
			t.Fatalf("Envoyer() numero %d = _, %v, want nil", i+1, err)
		}
	}

	gotChemins := p.chemins()
	wantChemins := []string{cheminLogin, cheminEnvoi, cheminEnvoi}
	if diff := cmp.Diff(wantChemins, gotChemins); diff != "" {
		t.Errorf("suite des appels, -want +got:\n%s", diff)
	}
}

// TestLExpirationDuJeton couvre le renouvellement proactif et sa marge.
//
// La marge de soixante secondes est ce qui évite un 401 systématique sur un jeton qui expire
// pendant le vol de la requête. Elle se vérifie en avançant l'horloge, jamais en dormant.
func TestLExpirationDuJeton(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom            string
		corpsLogin     string
		avance         time.Duration
		wantConnexions int
	}{
		{
			nom:        "jeton encore frais",
			corpsLogin: `{"data":{"token":"jeton-1","expires_in_seconds":3600}}`,
			avance:     30 * time.Minute, wantConnexions: 1,
		},
		{
			nom:        "jeton expire",
			corpsLogin: `{"data":{"token":"jeton-1","expires_in_seconds":3600}}`,
			avance:     61 * time.Minute, wantConnexions: 2,
		},
		{
			// Quatre-vingt-dix secondes annoncées, soixante de marge : le jeton est
			// considéré périmé après trente. Sans la marge, il serait réutilisé jusqu'à
			// la quatre-vingt-dixième et le second envoi essuierait un 401.
			nom:        "la marge devance l expiration reelle",
			corpsLogin: `{"data":{"token":"jeton-1","expires_in_seconds":90}}`,
			avance:     45 * time.Second, wantConnexions: 2,
		},
		{
			nom:        "duree de vie non annoncee, repli d une heure",
			corpsLogin: `{"data":{"token":"jeton-1"}}`,
			avance:     58 * time.Minute, wantConnexions: 1,
		},
		{
			nom:        "repli d une heure depasse",
			corpsLogin: `{"data":{"token":"jeton-1"}}`,
			avance:     61 * time.Minute, wantConnexions: 2,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t,
				[]reponse{{statut: http.StatusOK, corps: c.corpsLogin}},
				[]reponse{envoiOK("req-1")})
			h := nouvelleHorloge()

			client, err := ayospush.Nouveau(reglages(p),
				ayospush.AvecClientHTTP(p.srv.Client()), ayospush.AvecHorloge(h.maintenant))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			if _, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t)); err != nil {
				t.Fatalf("premier Envoyer() = _, %v, want nil", err)
			}
			h.avancer(c.avance)
			if _, err := client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t)); err != nil {
				t.Fatalf("second Envoyer() = _, %v, want nil", err)
			}

			var connexions int
			for _, chemin := range p.chemins() {
				if chemin == cheminLogin {
					connexions++
				}
			}
			if connexions != c.wantConnexions {
				t.Errorf("connexions = %d, want %d", connexions, c.wantConnexions)
			}
		})
	}
}

// TestDixEnvoisConcurrentsNeFontQuUneConnexion est le test qui prouve le verrou.
//
// Sans lui, un démarrage à froid sous trafic déclenche autant de connexions que de requêtes.
// Et si la passerelle n'admet qu'un seul jeton actif par clé, ces connexions s'invalident
// mutuellement : le service tourne alors en rond sans qu'aucun message ne parte. À lire sous
// -race, qui est obligatoire dans ce dépôt.
func TestDixEnvoisConcurrentsNeFontQuUneConnexion(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{envoiOK("req-1")})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	modele := modeleDeTest(t)
	var attente sync.WaitGroup
	echecs := make(chan error, 10)
	for range 10 {
		attente.Go(func() {
			if _, err := client.Envoyer(context.Background(), "+24166123456", modele); err != nil {
				echecs <- err
			}
		})
	}
	attente.Wait()
	close(echecs)
	for err := range echecs {
		t.Errorf("Envoyer() = _, %v, want nil", err)
	}

	var connexions, envois int
	for _, chemin := range p.chemins() {
		switch chemin {
		case cheminLogin:
			connexions++
		case cheminEnvoi:
			envois++
		}
	}
	if connexions != 1 {
		t.Errorf("connexions = %d, want 1", connexions)
	}
	if envois != 10 {
		t.Errorf("envois = %d, want 10", envois)
	}
}

// TestUnContexteAnnuleNeRejoueRien vérifie qu'une annulation ne devient pas une rafale.
func TestUnContexteAnnuleNeRejoueRien(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t,
		[]reponse{loginOK("jeton-1", 3600)},
		[]reponse{envoiOK("req-1")})

	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	ctx, annuler := context.WithCancel(context.Background())
	annuler()

	_, err = client.Envoyer(ctx, "+24166123456", modeleDeTest(t))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Envoyer() = _, %v, want une erreur qui est context.Canceled", err)
	}
	if got := p.chemins(); len(got) != 0 {
		t.Errorf("requetes emises = %v, want aucune", got)
	}
}

// TestUnNumeroInvalideNAppellePersonne vérifie que la garde du numéro précède le réseau.
//
// Un numéro qui n'est pas un E.164 canonique ne peut rien produire de bon chez le
// fournisseur : l'arrêter ici économise un aller-retour, une facture, et un 400 à déchiffrer.
func TestUnNumeroInvalideNAppellePersonne(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		numero string
	}{
		{nom: "vide", numero: ""},
		{nom: "avec des espaces", numero: "+241 66 12 34 56"},
		{nom: "avec des tirets", numero: "+241-66-12-34-56"},
		{nom: "avec des lettres", numero: "+241ABC12345"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t, []reponse{loginOK("jeton-1", 3600)}, []reponse{envoiOK("req-1")})
			client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			_, err = client.Envoyer(context.Background(), c.numero, modeleDeTest(t))
			if !errors.Is(err, ayospush.ErrRequeteRefusee) {
				t.Errorf("Envoyer(%q) = _, %v, want une erreur qui est ErrRequeteRefusee", c.numero, err)
			}
			if got := p.chemins(); len(got) != 0 {
				t.Errorf("requetes emises = %v, want aucune", got)
			}
		})
	}
}

// TestUneConnexionSansJetonEchoue couvre la réponse de connexion inexploitable.
func TestUneConnexionSansJetonEchoue(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "jeton absent", corps: `{"success":true,"data":{"expires_in_seconds":3600}}`},
		{nom: "jeton vide", corps: `{"success":true,"data":{"token":""}}`},
		{nom: "data absente", corps: `{"success":true}`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			p := nouvellePasserelle(t,
				[]reponse{{statut: http.StatusOK, corps: c.corps}},
				[]reponse{envoiOK("req-1")})

			client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
			if err != nil {
				t.Fatalf("Nouveau() = _, %v, want nil", err)
			}

			_, err = client.Envoyer(context.Background(), "+24166123456", modeleDeTest(t))
			if !errors.Is(err, ayospush.ErrAuthentification) {
				t.Errorf("Envoyer() = _, %v, want une erreur qui est ErrAuthentification", err)
			}
			// La connexion a échoué : aucun envoi n'a été tenté.
			gotChemins := p.chemins()
			if diff := cmp.Diff([]string{cheminLogin}, gotChemins); diff != "" {
				t.Errorf("suite des appels, -want +got:\n%s", diff)
			}
		})
	}
}

// TestUnModeleVideNEstPasEnvoye vérifie la garde de la valeur zéro.
func TestUnModeleVideNEstPasEnvoye(t *testing.T) {
	t.Parallel()

	p := nouvellePasserelle(t, []reponse{loginOK("jeton-1", 3600)}, []reponse{envoiOK("req-1")})
	client, err := ayospush.Nouveau(reglages(p), ayospush.AvecClientHTTP(p.srv.Client()))
	if err != nil {
		t.Fatalf("Nouveau() = _, %v, want nil", err)
	}

	if _, err := client.Envoyer(context.Background(), "+24166123456", whatsapp.Modele{}); err == nil {
		t.Error("Envoyer() avec un modele vide = _, nil, want une erreur")
	}
	if got := p.chemins(); len(got) != 0 {
		t.Errorf("requetes emises = %v, want aucune", got)
	}
}
