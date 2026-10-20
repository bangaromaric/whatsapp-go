package ayospush_test

import (
	"context"
	"fmt"
	"os"

	whatsapp "github.com/bangaromaric/whatsapp-go"
	"github.com/bangaromaric/whatsapp-go/ayospush"
)

// ExampleNouveau montre le câblage complet, exactement comme le README le donne.
//
// IL NE TOURNE PAS, et c'est délibéré : il n'a ni identifiants ni réseau, donc il ne porte
// aucun bloc « Output ». Go le COMPILE quand même, et c'est tout ce qu'on lui demande : que
// l'extrait du README ne puisse pas dériver de l'API sans que le build le dise. Les tests du
// paquet, eux, exercent le vrai dialogue contre un serveur simulé.
func ExampleNouveau() {
	client, err := ayospush.Nouveau(ayospush.Reglages{
		BaseURL:    "https://ayospush.com",
		CleAPI:     os.Getenv("WHATSAPP_API_KEY"),
		SecretAPI:  os.Getenv("WHATSAPP_API_SECRET"),
		Expediteur: os.Getenv("WHATSAPP_SENDER_ID"),
	})
	if err != nil {
		// Aucune requête n'a été émise : les réglages sont validés avant tout appel, pour
		// qu'un programme refuse de démarrer au lieu d'échouer au premier message.
		fmt.Println(err)
		return
	}

	// Le nom du gabarit vient de la configuration et jamais d'une constante compilée.
	modele, err := whatsapp.NouveauModele(os.Getenv("WHATSAPP_TEMPLATE"), "123456")
	if err != nil {
		fmt.Println(err)
		return
	}

	reference, err := client.Envoyer(context.Background(), "+24166123456", modele)
	if err != nil {
		fmt.Println(err)
		return
	}

	// La référence est la seule clé qui retrouve le message chez le fournisseur. À garder.
	fmt.Println(reference)
}
