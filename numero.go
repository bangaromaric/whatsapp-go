package whatsapp

import (
	"fmt"
	"net/url"
	"strings"
)

// hoteWhatsApp est l'hôte des liens de discussion WhatsApp.
const hoteWhatsApp = "https://wa.me/"

// NumeroAbsentError signale un destinataire sans numéro de téléphone.
//
// Elle existe parce que le numéro est souvent facultatif chez l'appelant : un carnet
// d'adresses tenu à la main porte des fiches sans téléphone, et c'est légitime. WhatsApp,
// lui, ne peut pas s'en passer, et l'appelant doit pouvoir distinguer ce cas d'une panne
// pour proposer d'ajouter le numéro plutôt que d'afficher un échec.
type NumeroAbsentError struct{}

func (NumeroAbsentError) Error() string { return "aucun numero de telephone pour ce destinataire" }

// Lien fabrique l'adresse qui ouvre WhatsApp sur une discussion, message pré-rempli.
//
// Le message n'est PAS envoyé : l'adresse ouvre la discussion sur le téléphone de
// l'utilisateur, texte déjà saisi, et c'est lui qui appuie sur envoyer. Aucun coût par
// message, aucun gabarit à faire approuver, et un texte libre, parce qu'il ne traverse
// aucune API.
//
// Les espaces sont encodés en %20 et non en plus, contrairement à ce que produirait
// url.Values.Encode. Le plus ne vaut espace que sous les règles d'encodage des formulaires
// web ; ce lien-ci est ouvert par une application mobile, qui n'a aucune raison de les
// appliquer. Un client qui le lirait littéralement afficherait « Bonjour+Mireille,+votre+
// rendez-vous+est+confirme », ce qui ruinerait le ton de n'importe quel message soigné. %20
// vaut espace dans les deux lectures, donc il n'y a rien à parier.
//
// QueryEscape échappe d'abord un plus littéral en %2B : le remplacement qui suit ne peut
// donc pas corrompre un message qui en contiendrait un.
func Lien(numero, message string) string {
	texte := strings.ReplaceAll(url.QueryEscape(message), "+", "%20")
	return hoteWhatsApp + numero + "?text=" + texte
}

// Numero rend la forme qu'attend un lien wa.me : des chiffres, et rien d'autre.
//
// Seul le plus est retiré, et c'est suffisant parce que ce qui arrive ici est un E.164
// canonique, d'où le préfixe national a déjà disparu. Au Gabon cela fait 241 suivi de huit
// chiffres.
//
// Une version antérieure gardait ce zéro dans l'E.164 et composait wa.me/241066123456, qui
// porte un chiffre de trop et ne désigne personne : les messages partaient dans le vide. Le
// correctif est en amont, chez qui produit l'E.164, et non ici : ce module n'a aucun plan de
// numérotation à connaître, et c'est ce qui le garde juste pour le pays suivant.
func Numero(e164 string) (string, error) {
	numero := strings.TrimPrefix(strings.TrimSpace(e164), "+")
	if numero == "" {
		return "", NumeroAbsentError{}
	}
	if strings.IndexFunc(numero, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", fmt.Errorf("numero %q: attendu des chiffres uniquement apres l indicatif", e164)
	}
	return numero, nil
}
