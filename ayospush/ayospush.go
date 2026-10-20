// Package ayospush parle le dialecte de la passerelle WhatsApp AyosPush.
//
// C'est la couche la plus externe de l'envoi, et la seule du module qui ouvre une connexion
// sortante. Elle ne porte aucune règle : elle traduit un modèle et un numéro en deux requêtes
// HTTP, et rend l'identifiant que la passerelle attribue au message. Changer de fournisseur,
// y compris pour l'API Cloud de Meta en direct, ne doit toucher que ce paquet.
//
// Deux particularités du contrat, à connaître avant de lire la suite, parce qu'elles sont la
// première cause d'échec au portage :
//
//   - la connexion est en JSON, l'envoi est en multipart. C'est contre-intuitif, et le type
//     de contenu se vérifie donc par point d'entrée et non par fournisseur ;
//   - le champ variables n'est pas un objet JSON dans un corps JSON : c'est une CHAÎNE
//     contenant du JSON, posée dans un champ de formulaire.
package ayospush

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	whatsapp "github.com/bangaromaric/whatsapp-go"
)

// Les motifs d'échec sur lesquels un appelant a une décision à prendre.
//
// Des sentinelles plates et interrogeables par errors.Is, et non une hiérarchie de types :
// ce qui intéresse l'appelant est « puis-je réessayer » et « est-ce ma faute », pas la
// généalogie de l'erreur.
var (
	// ErrConfiguration signale un réglage absent. Aucune requête n'a été émise.
	ErrConfiguration = errors.New("configuration de la passerelle incomplete")

	// ErrAuthentification signale des identifiants refusés, ou un jeton refusé deux fois
	// de suite. Un troisième essai échouerait à l'identique.
	ErrAuthentification = errors.New("la passerelle a refuse les identifiants")

	// ErrRequeteRefusee signale un refus de contrat : template inconnu, arité fausse,
	// numéro invalide. NE JAMAIS REJOUER : un rejeu échouerait à l'identique et
	// transformerait une faute de configuration en tempête de requêtes.
	ErrRequeteRefusee = errors.New("la passerelle a refuse la requete")

	// ErrQuota signale un débit dépassé. Rejouable plus tard, jamais tout de suite.
	//
	// ELLE EMBALLE [whatsapp.ErrQuotaAtteint], et c'est ce qui la rend exploitable hors
	// d'ici. Un appelant qui doit en tirer une phrase à l'écran interroge la sentinelle du
	// module d'envoi, sans avoir à importer ce paquet, donc sans apprendre le nom du
	// fournisseur pour écrire une phrase en français. Les deux restent interrogeables par
	// errors.Is sur la même erreur.
	//
	// Un type porteur plutôt qu'un errors.New enveloppé par %w, pour une seule raison : %w
	// aurait empilé les deux messages dans chaque journal, « quota de la passerelle
	// depasse: quota d envoi atteint », alors que la cause est une. Ce dépôt refuse les
	// messages empilés.
	ErrQuota error = quotaDepasse{}

	// ErrIndisponible signale une panne ou un délai dépassé. Rejouable.
	ErrIndisponible = errors.New("la passerelle est indisponible")

	// ErrSansReference signale une réponse 2xx dont on ne peut pas extraire
	// l'identifiant de message. C'est un ÉCHEC malgré le code de succès : sans cet
	// identifiant, le message est intraçable chez le fournisseur, donc ni réconciliable
	// ni opposable au support.
	ErrSansReference = errors.New("la passerelle a repondu sans identifiant de message")
)

// quotaDepasse porte [ErrQuota]. Voir la note de cette sentinelle pour le pourquoi du type.
type quotaDepasse struct{}

func (quotaDepasse) Error() string { return "quota de la passerelle depasse" }

// Unwrap rattache le motif du fournisseur à la sentinelle du module d'envoi.
func (quotaDepasse) Unwrap() error { return whatsapp.ErrQuotaAtteint }

// Les deux délais, et ils sont obligatoires.
//
// L'envoi est synchrone sur le chemin d'une requête entrante. Sans délai, une passerelle
// lente n'est pas un envoi lent : c'est un fil de traitement immobilisé, puis tous les fils,
// puis l'application entière qui ne répond plus pour une panne qui n'est pas la sienne.
const (
	delaiConnexion = 5 * time.Second
	delaiTotal     = 8 * time.Second
)

// Les deux réglages du cache de jeton.
const (
	// margeExpiration reconnecte avant l'expiration réelle. Sans elle, un jeton qui
	// expire pendant le vol de la requête provoque un 401 à chaque envoi.
	margeExpiration = 60 * time.Second

	// ttlRepli sert quand la passerelle n'annonce pas de durée de vie.
	ttlRepli = time.Hour
)

// Reglages porte ce que le client lit de son environnement.
type Reglages struct {
	// BaseURL est l'hôte de la passerelle, sans barre finale.
	BaseURL string

	// CleAPI identifie le compte applicatif.
	CleAPI string

	// SecretAPI est son mot de passe.
	SecretAPI string

	// Expediteur est le phone_number_id du numéro WhatsApp Business émetteur. Il est
	// semi-public chez Meta et se traite comme un secret : il identifie le compte.
	Expediteur string
}

// Client envoie des messages de template par la passerelle.
//
// Il garde son jeton entre deux envois. Il est sûr d'emploi concurrent, et il le doit : il
// est appelé depuis des requêtes HTTP.
type Client struct {
	http       *http.Client
	maintenant func() time.Time
	reglages   Reglages

	// mu garde le jeton, et le verrou est tenu PENDANT la connexion. Sans cela, un
	// démarrage à froid sous trafic déclenche autant de connexions que de requêtes ; et
	// si la passerelle n'admet qu'un seul jeton actif par clé, ces connexions
	// s'invalident mutuellement et aucune ne finit par aboutir. Le coût est nul : une
	// connexion a lieu une fois par heure.
	//
	// Le contrôle du cache et la connexion ont lieu sous le MÊME verrou, et c'est ce qui
	// évite une seconde connexion inutile : deux envois qui essuient un 401 en même temps
	// se présentent tous les deux, le premier reconnecte, et le second trouve le jeton
	// neuf au lieu de reconnecter à son tour.
	mu     sync.Mutex
	jeton  string
	expire time.Time
}

// Option règle un détail du client. Elles existent pour les tests, qui ont besoin de pointer
// un serveur simulé et d'avancer l'horloge sans attendre.
type Option func(*Client)

// AvecClientHTTP impose le client HTTP, délais compris.
func AvecClientHTTP(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// AvecHorloge impose la source du temps.
//
// L'expiration du jeton se teste ainsi sans time.Sleep, que les règles de test du dépôt
// interdisent comme moyen de synchronisation.
func AvecHorloge(maintenant func() time.Time) Option {
	return func(c *Client) {
		if maintenant != nil {
			c.maintenant = maintenant
		}
	}
}

// Nouveau câble le client et valide ses réglages.
//
// La validation a lieu ici et n'émet AUCUNE requête : se connecter pour découvrir qu'une clé
// est vide enverrait des identifiants vides sur le réseau, et le journal du fournisseur en
// garderait la trace. C'est aussi ce qui permet au programme appelant de refuser de démarrer
// plutôt que d'échouer au premier message qu'on lui demande d'envoyer.
func Nouveau(r Reglages, options ...Option) (*Client, error) {
	r.BaseURL = strings.TrimRight(strings.TrimSpace(r.BaseURL), "/")
	r.CleAPI = strings.TrimSpace(r.CleAPI)
	r.SecretAPI = strings.TrimSpace(r.SecretAPI)
	r.Expediteur = strings.TrimSpace(r.Expediteur)

	var manques []string
	if r.BaseURL == "" {
		manques = append(manques, "base-url")
	}
	if r.CleAPI == "" {
		manques = append(manques, "api-key")
	}
	if r.SecretAPI == "" {
		manques = append(manques, "api-secret")
	}
	if r.Expediteur == "" {
		manques = append(manques, "sender-id")
	}
	if len(manques) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrConfiguration, strings.Join(manques, ", "))
	}

	c := &Client{
		http:       clientParDefaut(),
		maintenant: time.Now,
		reglages:   r,
	}
	for _, o := range options {
		o(c)
	}
	return c, nil
}

// clientParDefaut construit un client HTTP aux deux délais bornés.
//
// Le même client sert la connexion et l'envoi : il mutualise les connexions TCP, et il permet
// à un unique serveur simulé d'intercepter les deux appels dans l'ordre, ce qui est ce qui
// rend les tests crédibles.
func clientParDefaut() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: delaiConnexion}).DialContext
	return &http.Client{Timeout: delaiTotal, Transport: transport}
}
