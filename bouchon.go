package whatsapp

import (
	"context"
	"crypto/rand"
	"log/slog"
	"strings"
)

// Bouchon est l'implémentation d'envoi qui n'envoie rien.
//
// Ce n'est pas un artefact de test : il sert au développement, à la démonstration et à tout
// essai de bout en bout qu'on ne veut pas voir arriver sur le téléphone de quelqu'un. Un
// poste de travail n'a ni identifiants de passerelle ni gabarit approuvé, et un envoi réel
// par erreur depuis une boucle de développement se paie en messages facturés chez de vraies
// personnes.
//
// PIÈGE À CONNAÎTRE, et il est le seul danger de ce type : un bouchon rend TOUJOURS un
// succès. Là où WhatsApp est le canal unique, par exemple pour un code d'authentification,
// un bouchon laissé actif en production ferait paraître le service fonctionnel alors que
// personne ne recevrait jamais rien.
//
// LA PARADE N'EST PAS DANS CE MODULE, et c'est délibéré : ce paquet ne connaît aucune notion
// d'environnement. C'est à l'appelant de refuser de démarrer en production quand l'envoi réel
// n'est pas activé, et non l'inverse.
//
// La valeur zéro journalise sur le journal par défaut.
type Bouchon struct {
	journal *slog.Logger
}

// NouveauBouchon câble le bouchon sur un journal.
//
// Un journal nul retombe sur slog.Default : le bouchon doit rester utilisable depuis un test
// qui ne se soucie pas de journalisation.
func NouveauBouchon(journal *slog.Logger) Bouchon { return Bouchon{journal: journal} }

// Envoyer journalise l'envoi qui aurait eu lieu et rend une référence factice.
//
// La référence est préfixée « bouchon- » pour qu'elle se reconnaisse d'un coup d'œil en base
// et dans un journal : une ligne qui porte cette marque n'est jamais partie, et personne n'a à
// le déduire d'une variable d'environnement d'il y a trois semaines.
//
// Le reste vient de crypto/rand.Text, vingt-six caractères base32 tirés au sort. Il ne rend
// AUCUNE erreur à traiter, et il n'ajoute aucune dépendance : ce module n'en a aucune hors
// bibliothèque standard, et un identifiant factice ne valait pas d'en ouvrir la première.
//
// Le numéro est masqué. Les valeurs des variables ne sont PAS journalisées : pour un code
// d'authentification, ce serait écrire un secret dans un journal que l'exploitant conserve.
func (b Bouchon) Envoyer(_ context.Context, numeroE164 string, m Modele) (Reference, error) {
	if m.Absent() {
		return Reference{}, EnvoiError{Operation: "modele absent"}
	}

	b.logger().Info("envoi whatsapp simule, aucun message n est parti",
		"destinataire", MasquerNumero(numeroE164),
		"template", m.Nom(),
		"variables", len(m.variables))

	return Reference{valeur: "bouchon-" + rand.Text()}, nil
}

func (b Bouchon) logger() *slog.Logger {
	if b.journal == nil {
		return slog.Default()
	}
	return b.journal
}

// MasquerNumero rend un numéro lisible dans un journal sans l'y écrire en clair.
//
// Un numéro de téléphone identifie une personne, et un journal de production se garde des
// mois et se lit par plus de monde qu'on ne croit. L'indicatif et les deux derniers chiffres
// suffisent à reconnaître une ligne au support quand on a déjà le numéro sous les yeux, et ne
// permettent pas de le reconstituer.
//
// La fonction est exportée pour L'APPELANT, qui est le seul à tenir un journal : ni ce
// bouchon, qui ne sert pas la production, ni l'adaptateur de passerelle, qui ne journalise
// rien du tout, n'en tiennent un. Deux masquages écrits séparément finiraient par différer,
// et le plus bavard des deux ferait la fuite.
func MasquerNumero(e164 string) string {
	numero := strings.TrimSpace(e164)
	// Quatre pour l'indicatif, deux pour la fin : en dessous de sept chiffres il ne reste
	// rien à masquer, et un numéro trop court est de toute façon invalide.
	if len(numero) < 7 {
		return "****"
	}
	return numero[:4] + "****" + numero[len(numero)-2:]
}
