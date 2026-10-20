package ayospush

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// cheminLogin est le point d'entrée d'authentification de la passerelle.
const cheminLogin = "/api/v1/auth/login"

// requeteLogin est le corps de la connexion. En JSON, contrairement à l'envoi.
type requeteLogin struct {
	CleAPI    string `json:"api_key"`
	SecretAPI string `json:"api_secret"`
}

// reponseLogin ne lit que ce qui sert à garder le jeton.
//
// Désérialiser l'intégralité de la réponse d'un fournisseur est une faute d'architecture :
// chaque champ ajouté ou renommé chez lui casserait alors la compilation chez nous, pour une
// donnée dont on n'a aucun usage.
//
// DEUX CHAMPS POUR LA MÊME CHOSE, et ce n'est pas de la gourmandise. Le guide d'intégration
// documente `expires_in_seconds` ; la passerelle réelle, mesurée le 8 octobre 2026, rend
// `expires_at`. Lire les deux évite de retomber sur un repli d'une heure devinée alors que la
// réponse annonce une durée exacte, et laisse le code juste si le fournisseur bascule d'une
// forme à l'autre.
type reponseLogin struct {
	Data struct {
		Token string `json:"token"`

		// LES DEUX CHAMPS DE DURÉE SONT LAISSÉS BRUTS, et c'est une leçon payée.
		//
		// Ils étaient typés, `*int64` pour l'un. La passerelle rend 3599.995884, un
		// flottant : le décodage de la STRUCTURE ENTIÈRE échouait, donc le jeton n'était
		// jamais lu, donc l'authentification du produit tombait, pour un champ qui n'est
		// qu'une optimisation du cache de jeton.
		//
		// Les laisser bruts déplace la tolérance au bon endroit. Le décodage ne peut plus
		// échouer que sur du JSON globalement illisible, et l'interprétation de la durée,
		// elle, a le droit de renoncer : l'appelant prend alors son repli d'une heure.
		TTLSecondes json.RawMessage `json:"expires_in_seconds"`

		// ExpireLe est l'instant d'expiration. Formes rencontrées : une date RFC 3339
		// avec décalage, un entier de secondes Unix, un entier de millisecondes.
		ExpireLe json.RawMessage `json:"expires_at"`
	} `json:"data"`
}

// ttlMax borne ce qu'on accepte comme durée de vie annoncée.
//
// Vingt-quatre heures. Une valeur plus grande n'est pas une bonne nouvelle, c'est le signe
// qu'on a mal lu : une date interprétée comme des secondes Unix, ou l'inverse, donne des
// durées absurdes. Mieux vaut alors le repli d'une heure, qui ne coûte qu'une reconnexion.
const ttlMax = 24 * time.Hour

// dureeDeVie rend la durée de vie annoncée du jeton, ou zéro si la réponse n'en dit rien
// d'exploitable.
//
// L'ordre des sources est celui de la confiance : une durée explicite d'abord, un instant
// d'expiration ensuite, et rien du tout en dernier, auquel cas l'appelant prend son repli.
func (r reponseLogin) dureeDeVie(maintenant time.Time) time.Duration {
	if secondes, connu := nombre(r.Data.TTLSecondes); connu {
		// Les secondes sont lues en flottant, parce que la passerelle en rend un :
		// 3599.995884. Les tronquer à l'entier ne coûterait rien ici, mais refuser un
		// flottant coûterait la connexion entière.
		return borner(time.Duration(secondes * float64(time.Second)))
	}
	if expire, connu := instant(r.Data.ExpireLe); connu {
		return borner(expire.Sub(maintenant))
	}
	return 0
}

// nombre lit une valeur numérique brute, entière ou flottante.
func nombre(brut json.RawMessage) (float64, bool) {
	if len(brut) == 0 {
		return 0, false
	}
	var lu float64
	if err := json.Unmarshal(brut, &lu); err != nil {
		return 0, false
	}
	return lu, true
}

// borner écarte une durée qui ne peut pas être celle d'un jeton.
func borner(d time.Duration) time.Duration {
	if d <= 0 || d > ttlMax {
		return 0
	}
	return d
}

// instant lit un instant d'expiration, quelle que soit la forme que le fournisseur emploie.
//
// Trois formes rencontrées chez les passerelles : un entier de secondes Unix, un entier de
// millisecondes, et une date en chaîne. Les millisecondes se reconnaissent à leur ordre de
// grandeur : un instant en secondes dépasserait l'an 33 000.
func instant(brut json.RawMessage) (time.Time, bool) {
	if len(brut) == 0 {
		return time.Time{}, false
	}

	if horodatage, connu := nombre(brut); connu {
		if horodatage <= 0 {
			return time.Time{}, false
		}
		if horodatage > 1e12 {
			return time.UnixMilli(int64(horodatage)), true
		}
		return time.Unix(int64(horodatage), 0), true
	}

	var texte string
	if err := json.Unmarshal(brut, &texte); err != nil {
		return time.Time{}, false
	}
	// Le fuseau n'est pas toujours porté par la date. Sans lui, UTC est la seule lecture
	// défendable : supposer le fuseau du serveur donnerait une durée fausse d'une heure au
	// Gabon, donc un jeton réputé mort une heure trop tôt ou trop tard.
	for _, forme := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if lu, err := time.Parse(forme, texte); err == nil {
			return lu, true
		}
	}
	return time.Time{}, false
}

// jetonValide rend un jeton utilisable, en se connectant si besoin.
//
// perime, non vide, est le jeton qu'un 401 vient de disqualifier : il n'est pas réutilisé,
// même s'il est encore dans sa durée de vie annoncée. C'est le renouvellement RÉACTIF, et il
// est nécessaire en plus du proactif : une passerelle invalide parfois un jeton avant son
// terme, et s'en tenir au TTL annoncé bouclerait sur des 401.
func (c *Client) jetonValide(ctx context.Context, perime string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.jeton != "" && c.jeton != perime && c.maintenant().Before(c.expire) {
		return c.jeton, nil
	}
	return c.seConnecter(ctx)
}

// seConnecter échange les identifiants contre un jeton. À appeler verrou tenu.
func (c *Client) seConnecter(ctx context.Context) (string, error) {
	corps, err := json.Marshal(requeteLogin{CleAPI: c.reglages.CleAPI, SecretAPI: c.reglages.SecretAPI})
	if err != nil {
		return "", fmt.Errorf("composer la connexion: %w", err)
	}

	requete, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.reglages.BaseURL+cheminLogin, bytes.NewReader(corps))
	if err != nil {
		return "", fmt.Errorf("preparer la connexion: %w", err)
	}
	requete.Header.Set("Content-Type", "application/json")
	requete.Header.Set("Accept", "application/json")

	reponse, err := c.http.Do(requete)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrIndisponible, err)
	}
	defer fermer(reponse)

	if reponse.StatusCode < 200 || reponse.StatusCode > 299 {
		return "", classer(reponse)
	}

	var lue reponseLogin
	if err := json.NewDecoder(reponse.Body).Decode(&lue); err != nil {
		return "", fmt.Errorf("%w: lire la reponse de connexion: %w", ErrIndisponible, err)
	}
	if lue.Data.Token == "" {
		return "", fmt.Errorf("%w: aucun jeton exploitable dans la reponse", ErrAuthentification)
	}

	// La marge est retranchée de la durée annoncée, et le résultat est borné à zéro : une
	// passerelle qui annoncerait trente secondes donnerait sinon une expiration dans le
	// passé, donc une reconnexion à chaque envoi.
	maintenant := c.maintenant()
	ttl := lue.dureeDeVie(maintenant)
	if ttl == 0 {
		ttl = ttlRepli
	}
	if ttl -= margeExpiration; ttl < 0 {
		ttl = 0
	}

	c.jeton = lue.Data.Token
	c.expire = maintenant.Add(ttl)
	return c.jeton, nil
}
