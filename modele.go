package whatsapp

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// longueurMaxVariable borne la valeur d'une variable de template.
//
// Le plafond réel dépend du template approuvé, entre soixante et cent vingt caractères
// selon sa rédaction. Cent vingt est le haut de cette fourchette : au-delà, la plateforme
// refuse le message, et son refus arrive avec un message d'erreur qui ne nomme pas la
// variable coupable.
const longueurMaxVariable = 120

// longueurMaxReference borne l'identifiant de message rendu par la passerelle.
//
// Deux cent cinquante-cinq, et surtout AUCUNE troncature : un identifiant raboté reste
// lisible dans un journal mais ne retrouve plus rien chez le fournisseur, donc la
// réconciliation d'un message perdu devient impossible au moment précis où on en a besoin.
// L'implémentation de référence le tronque en silence à cent vingt ; c'est le défaut que ce
// plafond évite, en refusant plutôt qu'en coupant.
const longueurMaxReference = 255

// Modele désigne un template approuvé et les valeurs de ses variables.
//
// C'est le seul type que le port d'envoi accepte, et ce n'est pas une préférence de style.
// WhatsApp n'est pas un canal de texte libre : hors de la fenêtre de service de vingt-quatre
// heures, c'est à dire dès qu'aucun message entrant récent du destinataire n'existe, seul un
// template préapprouvé peut partir. Un port qui prendrait une chaîne laisserait croire qu'un
// texte arbitraire peut être envoyé, et le mensonge n'éclaterait qu'en production, au premier
// message.
//
// Le code ne connaît du template que son NOM et son ARITÉ. Le gabarit lui-même vit chez Meta,
// hors du dépôt : son nom est donc une donnée de configuration et jamais une constante
// compilée, pour qu'un template renommé ou versionné ne demande pas de redéployer le binaire.
//
// La valeur zéro est un modèle vide, et Envoyer le refuse. Un modèle se construit par
// NouveauModele, qui porte les invariants.
type Modele struct {
	nom       string
	variables []string
}

// NouveauModele décrit un envoi, en validant ce que la plateforme n'explique pas.
//
// Les variables sont POSITIONNELLES : leur ordre est l'ordre des {{1}}, {{2}}, … du gabarit.
// Elles n'acceptent que des valeurs courtes et sur une seule ligne. Un saut de ligne ou une
// tabulation fait rejeter le message par la plateforme, avec une erreur peu explicite : le
// contrôle a donc lieu ici, avant la moindre requête, pour que l'échec nomme la variable.
//
// Une arité fausse, elle, ne peut pas se vérifier ici : seul Meta connaît le gabarit. Elle se
// vérifie une fois, à la main, par le curl de mise en service.
func NouveauModele(nom string, variables ...string) (Modele, error) {
	nom = strings.TrimSpace(nom)
	if nom == "" {
		return Modele{}, fmt.Errorf("nom de template absent")
	}

	for i, v := range variables {
		// L'index affiché est celui du gabarit, donc compté depuis un, et non l'index de
		// la tranche : c'est le numéro que le rédacteur du template a sous les yeux.
		switch {
		case strings.TrimSpace(v) == "":
			return Modele{}, fmt.Errorf("variable %d du template %q vide", i+1, nom)
		case strings.ContainsAny(v, "\n\r\t"):
			return Modele{}, fmt.Errorf(
				"variable %d du template %q: saut de ligne ou tabulation interdit", i+1, nom)
		// L'UTF-8 invalide est refusé ICI et nulle part ailleurs, et c'est nécessaire :
		// encoding/json ne le refuse pas, il remplace chaque octet illisible par un
		// losange. Le message partirait donc, facturé, avec des losanges à la place du
		// nom du client. Mesuré : "A\xffB" s'encode en "A�B" sans aucune erreur.
		case !utf8.ValidString(v):
			return Modele{}, fmt.Errorf(
				"variable %d du template %q: utf-8 invalide", i+1, nom)
		case utf8.RuneCountInString(v) > longueurMaxVariable:
			return Modele{}, fmt.Errorf("variable %d du template %q: %d caracteres, maximum %d",
				i+1, nom, utf8.RuneCountInString(v), longueurMaxVariable)
		}
	}

	return Modele{nom: nom, variables: slices.Clone(variables)}, nil
}

// Nom rend le nom du template approuvé.
func (m Modele) Nom() string { return m.nom }

// Variables rend les valeurs dans l'ordre des {{n}} du gabarit.
//
// La tranche est copiée : un adaptateur qui la trierait ou la réécrirait sur place
// changerait le modèle de son appelant, et un modèle est une valeur.
func (m Modele) Variables() []string { return slices.Clone(m.variables) }

// Absent dit si le modèle est la valeur zéro.
func (m Modele) Absent() bool { return m.nom == "" }

// Reference est l'identifiant qu'une passerelle rend pour un message accepté.
//
// C'est la seule clé qui permette de retrouver le message chez le fournisseur : support,
// réconciliation, et accusé de livraison le jour où un webhook existera. Un envoi dont on ne
// peut pas extraire cet identifiant doit donc être traité comme un ÉCHEC, même sur un code
// 2xx : sans elle, le message est intraçable.
//
// La valeur zéro est une référence absente.
type Reference struct {
	valeur string
}

// NouvelleReference valide l'identifiant rendu par la passerelle.
func NouvelleReference(valeur string) (Reference, error) {
	valeur = strings.TrimSpace(valeur)
	if valeur == "" {
		return Reference{}, fmt.Errorf("reference de message vide")
	}
	if len(valeur) > longueurMaxReference {
		return Reference{}, fmt.Errorf("reference de message de %d octets, maximum %d",
			len(valeur), longueurMaxReference)
	}
	return Reference{valeur: valeur}, nil
}

// String rend la référence telle que le fournisseur l'a donnée.
func (r Reference) String() string { return r.valeur }

// Absente dit si la référence est la valeur zéro.
func (r Reference) Absente() bool { return r.valeur == "" }

// ErrQuotaAtteint signale un envoi refusé parce que la capacité de la passerelle est
// épuisée, et non parce que le message ou le numéro serait fautif.
//
// ELLE VIT ICI ET NON DANS LE PAQUET DU FOURNISSEUR, et c'est ce qui la rend utilisable.
// Un appelant qui voudrait distinguer ce cas en interrogeant la sentinelle d'AyosPush
// devrait importer AyosPush, c'est à dire apprendre le nom du fournisseur pour écrire une
// phrase à l'écran. La saturation est un fait du canal, pas de ce fournisseur : chaque
// passerelle l'emballe dans son propre motif, et l'appelant n'interroge que celle-ci.
//
// Elle ne porte aucune catégorie d'erreur applicative, pour la raison qu'[EnvoiError]
// développe juste en dessous : c'est à l'appelant de décider ce que la saturation vaut pour
// lui. Il est le seul à pouvoir le faire, parce qu'il est le seul à savoir pourquoi il
// envoyait.
//
// Rejouable, mais jamais tout de suite : le palier d'un portefeuille WhatsApp se compte en
// destinataires distincts par vingt-quatre heures, donc l'attente utile est de l'ordre de
// l'heure et non de la seconde.
var ErrQuotaAtteint = errors.New("quota d envoi atteint")

// EnvoiError signale qu'un message n'a pas pu partir.
//
// Ce n'est PAS une erreur de domaine, et la distinction porte à conséquence : elle n'emballe
// aucune catégorie d'erreur applicative, donc rien ne la convertit en code métier ni en code
// HTTP. Un échec de transport n'est pas une règle violée par l'utilisateur, et c'est à
// l'appelant, qui sait pourquoi il envoyait, de décider ce que l'échec vaut pour lui.
type EnvoiError struct {
	// Operation nomme ce qui a échoué, en minuscules et sans ponctuation finale.
	Operation string

	// Err est la cause, souvent une erreur de transport ou un code HTTP inattendu. Elle
	// peut être nulle quand l'échec vient d'une configuration absente.
	Err error
}

// Error rend le message d'erreur.
func (e EnvoiError) Error() string {
	if e.Err == nil {
		return "envoi whatsapp: " + e.Operation
	}
	return fmt.Sprintf("envoi whatsapp: %s: %v", e.Operation, e.Err)
}

// Unwrap rend la cause, pour que errors.Is et errors.As traversent.
func (e EnvoiError) Unwrap() error { return e.Err }
