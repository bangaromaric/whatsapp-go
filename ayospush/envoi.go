package ayospush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	whatsapp "github.com/bangaromaric/whatsapp-go"
)

// cheminEnvoi est le point d'entrée d'envoi de la passerelle.
const cheminEnvoi = "/api/v1/messages/template"

// longueurMaxRaison borne ce qu'on recopie du corps d'erreur du fournisseur.
//
// Assez pour nommer la cause dans un journal, pas assez pour y déverser une page HTML
// d'erreur de plusieurs kilo-octets à chaque incident.
const longueurMaxRaison = 256

// reponseEnvoi ne lit que l'identifiant de message.
//
// Le champ status n'est pas lu, et c'est délibéré : « queued » veut dire accepté pour
// émission, pas remis. Le lire inviterait à en tirer une conclusion sur la livraison, que
// seul un accusé de réception pourrait donner. Les champs recipient, template et message ne
// sont pas lus non plus : ils répètent ce qu'on vient d'envoyer.
//
// L'IDENTIFIANT EST LAISSÉ BRUT, et c'est la troisième leçon du même ordre. Le guide
// d'intégration le montre en chaîne (« req-9ÂÂ ») ; la passerelle rend un ENTIER, 9168. Un champ
// typé `string` faisait échouer le décodage de toute la réponse, donc l'envoi était rapporté
// en échec alors que le message était bel et bien parti. Le pire des deux mondes : le
// destinataire reçoit son message, et le serveur dit non.
type reponseEnvoi struct {
	Data struct {
		RequestID json.RawMessage `json:"request_id"`
	} `json:"data"`
}

// referenceDe rend l'identifiant de message sous forme de texte, quelle que soit sa forme.
//
// La tolérance porte sur le TYPE du champ, jamais sur son absence : un 2xx dont on ne peut
// extraire aucun identifiant reste un échec, parce qu'un message qu'on ne peut pas retrouver
// chez le fournisseur ne sert ni au support ni à la réconciliation.
//
// Pour un nombre, le jeton JSON brut EST déjà sa forme textuelle : 9168 devient « 9168 », sans
// passer par un flottant qui écrirait « 9168.000000 » ou perdrait des chiffres sur un
// identifiant long.
func referenceDe(brut json.RawMessage) (string, bool) {
	texte := strings.TrimSpace(string(brut))
	if texte == "" || texte == "null" {
		return "", false
	}

	// Une chaîne JSON se dés-échappe proprement plutôt que de se faire retirer ses
	// guillemets à la main : un identifiant peut contenir une séquence échappée.
	if texte[0] == '"' {
		var lu string
		if err := json.Unmarshal(brut, &lu); err != nil {
			return "", false
		}
		return lu, lu != ""
	}

	// Tout le reste n'est un identifiant que si c'est un scalaire. Un objet ou un tableau
	// ne se laisse pas lire comme une référence, et le prendre pour tel écrirait du JSON
	// en base à la place d'un identifiant.
	if texte[0] == '{' || texte[0] == '[' {
		return "", false
	}
	return texte, true
}

// Envoyer fait partir un message de template et rend l'identifiant que la passerelle attribue.
//
// Le numéro entre au format E.164 canonique, avec le plus : c'est la forme que ce module
// attend partout. La passerelle le veut sans, et cette conversion appartient à cette couche et
// à aucune autre, parce qu'elle est une propriété du fournisseur et non de l'appelant.
//
// UN SEUL REJEU, et seulement sur 401 ou 403. Au deuxième refus consécutif, le problème n'est
// plus le jeton mais les identifiants ou les droits, et rejouer davantage masquerait une faute
// de configuration derrière une rafale de requêtes. Un 400 ou un 422 n'est jamais rejoué : il
// échouerait à l'identique.
func (c *Client) Envoyer(ctx context.Context, numeroE164 string, m whatsapp.Modele) (whatsapp.Reference, error) {
	if m.Absent() {
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: "modele absent"}
	}

	destinataire, err := whatsapp.Numero(numeroE164)
	if err != nil {
		// Un numéro que le fournisseur refuserait est une faute de l'appelant, pas une
		// indisponibilité : il est classé comme tel pour que personne ne le rejoue.
		return whatsapp.Reference{}, whatsapp.EnvoiError{
			Operation: "destinataire",
			Err:       fmt.Errorf("%w: %w", ErrRequeteRefusee, err),
		}
	}

	// Le corps est composé UNE FOIS et renvoyé tel quel au rejeu, depuis un lecteur neuf.
	// Réutiliser le même io.Reader enverrait un corps VIDE au second essai, et la
	// passerelle rendrait un 400 que rien dans le code n'expliquerait.
	corps, typeContenu, err := c.corps(destinataire, m)
	if err != nil {
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: "composer l envoi", Err: err}
	}

	jeton, err := c.jetonValide(ctx, "")
	if err != nil {
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: "connexion", Err: err}
	}

	reference, err := c.poster(ctx, corps, typeContenu, jeton)
	if err == nil {
		return reference, nil
	}
	if !errors.Is(err, ErrAuthentification) {
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: "envoi", Err: err}
	}

	// Le jeton part en « périmé » : la reconnexion ne le réutilisera pas, même s'il est
	// encore dans sa durée de vie annoncée.
	frais, err := c.jetonValide(ctx, jeton)
	if err != nil {
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: "renouvellement du jeton", Err: err}
	}

	reference, err = c.poster(ctx, corps, typeContenu, frais)
	if err != nil {
		const operation = "envoi apres renouvellement"
		// Un second refus de jeton n'est plus un jeton périmé : ce sont les droits.
		if errors.Is(err, ErrAuthentification) {
			return whatsapp.Reference{}, whatsapp.EnvoiError{
				Operation: operation,
				Err:       fmt.Errorf("%w: refuse deux fois de suite", ErrAuthentification),
			}
		}
		return whatsapp.Reference{}, whatsapp.EnvoiError{Operation: operation, Err: err}
	}
	return reference, nil
}

// poster émet une tentative d'envoi.
func (c *Client) poster(ctx context.Context, corps []byte, typeContenu, jeton string) (whatsapp.Reference, error) {
	requete, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.reglages.BaseURL+cheminEnvoi, bytes.NewReader(corps))
	if err != nil {
		return whatsapp.Reference{}, fmt.Errorf("preparer l envoi: %w", err)
	}
	requete.Header.Set("Authorization", "Bearer "+jeton)
	requete.Header.Set("Content-Type", typeContenu)
	requete.Header.Set("Accept", "application/json")

	reponse, err := c.http.Do(requete)
	if err != nil {
		return whatsapp.Reference{}, fmt.Errorf("%w: envoi: %w", ErrIndisponible, err)
	}
	defer fermer(reponse)

	if reponse.StatusCode < 200 || reponse.StatusCode > 299 {
		return whatsapp.Reference{}, classer(reponse)
	}

	var lue reponseEnvoi
	if err := json.NewDecoder(reponse.Body).Decode(&lue); err != nil {
		return whatsapp.Reference{}, fmt.Errorf("%w: lire la reponse d envoi: %w", ErrSansReference, err)
	}

	texte, lisible := referenceDe(lue.Data.RequestID)
	if !lisible {
		return whatsapp.Reference{}, fmt.Errorf("%w: champ data.request_id absent ou illisible",
			ErrSansReference)
	}

	reference, err := whatsapp.NouvelleReference(texte)
	if err != nil {
		return whatsapp.Reference{}, fmt.Errorf("%w: %w", ErrSansReference, err)
	}
	return reference, nil
}

// corps compose le multipart de l'envoi.
//
// Le type de contenu vient de FormDataContentType et n'est JAMAIS écrit à la main : il porte
// la limite de séparation que l'écrivain vient de tirer, et une limite écrite à la main ne
// correspondrait à rien dans le corps.
func (c *Client) corps(destinataire string, m whatsapp.Modele) ([]byte, string, error) {
	variables, err := variablesJSON(m.Variables())
	if err != nil {
		return nil, "", err
	}

	var tampon bytes.Buffer
	ecrivain := multipart.NewWriter(&tampon)
	champs := [][2]string{
		{"phone_number_id", c.reglages.Expediteur},
		{"recipient_number", destinataire},
		{"template_name", m.Nom()},
		{"variables", variables},
	}
	for _, champ := range champs {
		if err := ecrivain.WriteField(champ[0], champ[1]); err != nil {
			return nil, "", fmt.Errorf("ecrire le champ %s: %w", champ[0], err)
		}
	}
	// La fermeture écrit la frontière terminale, et elle doit précéder la lecture du
	// tampon : sans elle, le corps est tronqué et la passerelle ne voit aucun champ.
	if err := ecrivain.Close(); err != nil {
		return nil, "", fmt.Errorf("clore le multipart: %w", err)
	}
	return tampon.Bytes(), ecrivain.FormDataContentType(), nil
}

// variablesJSON sérialise les variables en objet indexé depuis 1, dans l'ordre du modèle.
//
// Écrit à la main, et PAS par json.Marshal d'une map, pour une raison qui ne se voit qu'en
// production : encoding/json ordonne les clés d'une map lexicographiquement, donc « 10 » se
// range entre « 1 » et « 2 ». À partir de dix variables, la passerelle substituerait les
// valeurs aux mauvaises places, et le message partirait, faux. L'ordre est garanti deux fois
// ici : les variables sont une tranche, et le JSON est bâti en la parcourant.
//
// Chaque valeur passe par json.Marshal, qui échappe les guillemets, les accents et les
// caractères de contrôle. Il ne refuse PAS l'UTF-8 invalide, il le remplace par des losanges
// sans rien dire : c'est pourquoi la validité de l'encodage est contrôlée en amont, par
// whatsapp.NouveauModele, et pas ici. L'erreur rendue ici ne couvre donc qu'un cas qui ne
// devrait jamais arriver, et elle existe parce qu'avaler une erreur sur un Close ou un Marshal
// est précisément ce que les règles du dépôt interdisent.
func variablesJSON(variables []string) (string, error) {
	var sortie strings.Builder
	sortie.WriteByte('{')
	for i, valeur := range variables {
		if i > 0 {
			sortie.WriteByte(',')
		}
		encodee, err := json.Marshal(valeur)
		if err != nil {
			return "", fmt.Errorf("%w: variable %d: %w", ErrRequeteRefusee, i+1, err)
		}
		sortie.WriteByte('"')
		sortie.WriteString(strconv.Itoa(i + 1))
		sortie.WriteString("\":")
		sortie.Write(encodee)
	}
	sortie.WriteByte('}')
	return sortie.String(), nil
}

// classer traduit un code de retour en motif d'échec interrogeable.
//
// La distinction qui compte est celle entre ce qui se rejoue et ce qui ne se rejoue jamais.
// Un inconnu est traité comme une indisponibilité : c'est le défaut le moins coûteux, parce
// qu'un rejeu différé sur une erreur de contrat se voit dans les journaux, alors qu'un refus
// définitif sur une panne passagère perd un message pour rien.
func classer(reponse *http.Response) error {
	raison := lireRaison(reponse.Body)

	var motif error
	switch {
	case reponse.StatusCode == http.StatusUnauthorized, reponse.StatusCode == http.StatusForbidden:
		motif = ErrAuthentification
	case reponse.StatusCode == http.StatusTooManyRequests:
		motif = ErrQuota
	case reponse.StatusCode >= 400 && reponse.StatusCode < 500:
		motif = ErrRequeteRefusee
	default:
		motif = ErrIndisponible
	}

	if raison == "" {
		return fmt.Errorf("%w: statut %d", motif, reponse.StatusCode)
	}
	return fmt.Errorf("%w: statut %d: %s", motif, reponse.StatusCode, raison)
}

// lireRaison recopie le début du corps d'erreur, pour le journal.
func lireRaison(corps io.Reader) string {
	brut, err := io.ReadAll(io.LimitReader(corps, longueurMaxRaison))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(brut), "\n", " "))
}

// fermer vide et ferme un corps de réponse.
//
// La lecture jusqu'au bout avant la fermeture est ce qui permet au transport de réutiliser la
// connexion : abandonner un corps à moitié lu la fait jeter, et chaque envoi repaie alors une
// poignée de main TLS.
func fermer(reponse *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reponse.Body, 1<<16))
	_ = reponse.Body.Close()
}
