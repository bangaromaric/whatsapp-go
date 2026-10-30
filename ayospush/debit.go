package ayospush

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	whatsapp "github.com/bangaromaric/whatsapp-go"
)

// Les en-têtes par lesquels la passerelle parle de son propre bridage.
//
// X-RateLimit-Limit est MESURÉ : la passerelle a rendu « 5 » le 13 octobre 2026. Les deux
// autres sont lus parce qu'ils sont les formes usuelles du même propos, et leur absence ne
// coûte rien : tout ce qui manque laisse l'appelant sur son propre repli.
const (
	enteteLimite  = "X-RateLimit-Limit"
	enteteReprise = "Retry-After"
	enteteFenetre = "X-RateLimit-Reset"
)

// lireDebit traduit les en-têtes de bridage en une valeur que l'appelant comprend.
//
// RIEN ICI NE FAIT ÉCHOUER UN ENVOI, et c'est la règle qui gouverne tout ce fichier. Un
// en-tête absent, mal formé ou absurde laisse sa valeur à zéro. C'est la même tolérance que
// la durée de vie du jeton, et pour la même raison, apprise au même endroit : ces en-têtes
// sont une AIDE et non un contrat, et refuser une réponse parce qu'une aide est illisible
// échangerait un petit bénéfice contre une panne.
func lireDebit(entetes http.Header, maintenant time.Time) whatsapp.Debit {
	return whatsapp.Debit{
		Limite:  limiteAnnoncee(entetes.Get(enteteLimite)),
		Reprise: repriseAnnoncee(entetes, maintenant),
	}
}

// limiteAnnoncee lit le nombre d'envois par fenêtre. Zéro si la passerelle n'en dit rien.
func limiteAnnoncee(brut string) int {
	limite, err := strconv.Atoi(strings.TrimSpace(brut))
	if err != nil || limite <= 0 {
		return 0
	}
	return limite
}

// repriseAnnoncee rend le délai avant réouverture, par ordre de confiance décroissant.
//
// Retry-After D'ABORD, parce que c'est une INSTRUCTION : la norme la définit, et la passerelle
// l'adresse au client pour lui dire quoi faire. X-RateLimit-Reset ensuite, parce qu'il décrit
// une fenêtre et non une conduite à tenir, et que sa forme n'est normalisée nulle part. Rien
// enfin, et c'est l'appelant qui décide.
//
// Une valeur inexploitable ne s'arrête pas à son étage : elle laisse l'étage suivant répondre.
// Une passerelle qui rend « Retry-After: 0 » ne doit pas faire perdre un X-RateLimit-Reset
// parfaitement lisible posé juste à côté.
func repriseAnnoncee(entetes http.Header, maintenant time.Time) time.Duration {
	if delai := apresDelai(entetes.Get(enteteReprise), maintenant); delai > 0 {
		return delai
	}
	return apresReouverture(entetes.Get(enteteFenetre), maintenant)
}

// apresDelai lit un Retry-After, sous les DEUX formes que la norme autorise.
//
// Un nombre de secondes, ou une date HTTP. Les deux sont licites, et une passerelle peut
// passer de l'une à l'autre sans rien annoncer : n'en lire qu'une ferait retomber sur le délai
// deviné la moitié du temps, en silence, ce qui est le pire des deux mondes puisque le code
// aurait l'air de lire l'en-tête.
//
// http.ParseTime couvre les trois formes de date que la norme admet, et c'est pourquoi la date
// n'est pas analysée à la main.
func apresDelai(brut string, maintenant time.Time) time.Duration {
	brut = strings.TrimSpace(brut)
	if brut == "" {
		return 0
	}
	if secondes, err := strconv.Atoi(brut); err == nil {
		// LE PLAFOND EST APPLIQUÉ AVANT LA MULTIPLICATION, et ce n'est pas une précaution
		// de principe. time.Duration compte des nanosecondes : multiplier un entier déjà
		// démesuré déborde en silence, et le débordement ne rend pas toujours une valeur
		// visiblement fausse. Mesuré : « Retry-After: 18446744074 », soit cinq cent
		// quatre-vingt-quatre ans, devient 290 ms, que borner accepterait pour vraie.
		// L'appelant rejouerait alors tout de suite une passerelle qui demandait de
		// reculer très fort, ce qui est exactement la conduite qu'un palier punit.
		if secondes < 0 || secondes > int(dureeMax/time.Second) {
			return 0
		}
		return borner(time.Duration(secondes) * time.Second)
	}
	if date, err := http.ParseTime(brut); err == nil {
		return borner(date.Sub(maintenant))
	}
	return 0
}

// apresReouverture lit un X-RateLimit-Reset, dont la forme n'est normalisée nulle part.
//
// Trois formes circulent, et elles se distinguent À L'ORDRE DE GRANDEUR, exactement comme
// l'instant d'expiration du jeton plus haut : un nombre de secondes à attendre, un instant
// Unix en secondes, un instant Unix en millisecondes.
//
// LES SEUILS SONT CHOISIS POUR QU'AUCUNE VALEUR PLAUSIBLE NE SOIT LUE DE TRAVERS. Un délai
// d'attente devrait dépasser trente ans avant d'être pris pour un instant, et un instant
// devrait être antérieur à 2001 avant d'être pris pour un délai. Entre les deux, il n'y a rien
// à parier.
func apresReouverture(brut string, maintenant time.Time) time.Duration {
	valeur, err := strconv.ParseInt(strings.TrimSpace(brut), 10, 64)
	if err != nil || valeur <= 0 {
		return 0
	}
	switch {
	case valeur > 1e12:
		return borner(time.UnixMilli(valeur).Sub(maintenant))
	case valeur > 1e9:
		return borner(time.Unix(valeur, 0).Sub(maintenant))
	default:
		return borner(time.Duration(valeur) * time.Second)
	}
}

// signaler fait suivre le bridage annoncé à l'observateur, s'il y en a un.
//
// Le silence est le défaut : sans observateur, ou sans rien d'annoncé, rien n'est appelé. Un
// appelant qui journalise au fil des rappels n'écrit donc une ligne que quand la passerelle a
// réellement parlé.
func (c *Client) signaler(debit whatsapp.Debit) {
	if c.observateur == nil || !debit.Annonce() {
		return
	}
	c.observateur(debit)
}
