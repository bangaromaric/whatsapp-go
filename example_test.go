package whatsapp_test

import (
	"fmt"

	whatsapp "github.com/bangaromaric/whatsapp-go"
)

// Ces exemples existent pour deux raisons, et la seconde compte autant que la première.
//
// Ils montrent l'usage dans la godoc, et ils FONT COMPILER ET TOURNER le code que le README
// donne. Un extrait de documentation que rien ne vérifie finit par mentir, et le mensonge est
// alors découvert par celui qui intègre le module, pas par celui qui l'écrit.

// ExampleLien montre le mécanisme gratuit : une adresse que l'utilisateur ouvre lui-même.
func ExampleLien() {
	numero, err := whatsapp.Numero("+24166123456")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(whatsapp.Lien(numero, "Bonjour Mireille, votre rendez-vous est confirme"))
	// Output:
	// https://wa.me/24166123456?text=Bonjour%20Mireille%2C%20votre%20rendez-vous%20est%20confirme
}

// ExampleNumero montre la seule transformation que le module s'autorise sur un numéro.
//
// L'entrée est un E.164 canonique, avec le plus et sans le zéro national. La sortie est ce
// qu'attend un lien wa.me : des chiffres, et rien d'autre.
func ExampleNumero() {
	numero, err := whatsapp.Numero("+24166123456")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(numero)
	// Output:
	// 24166123456
}

// ExampleNouveauModele montre le mécanisme payant : un gabarit approuvé et ses variables.
//
// Le nom vient de la configuration et jamais d'une constante compilée : la passerelle renomme
// les gabarits qu'on lui soumet, en collant un suffixe de compte et d'horodatage.
func ExampleNouveauModele() {
	modele, err := whatsapp.NouveauModele("mon_gabarit_biz1_1013165939", "123456")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(modele.Nom())
	fmt.Println(modele.Variables())
	// Output:
	// mon_gabarit_biz1_1013165939
	// [123456]
}

// ExampleNouveauModele_sautDeLigne montre le refus qui fait gagner le plus de temps.
//
// La plateforme rejette une variable multiligne avec une erreur qui ne nomme ni la variable
// ni la raison. Le refus a donc lieu ici, avant la moindre requête, et il nomme les deux.
func ExampleNouveauModele_sautDeLigne() {
	_, err := whatsapp.NouveauModele("mon_gabarit", "Mireille\nMba")

	fmt.Println(err)
	// Output:
	// variable 1 du template "mon_gabarit": saut de ligne ou tabulation interdit
}

// ExampleMasquerNumero montre comment tracer un envoi sans écrire un numéro en clair.
//
// L'indicatif et les deux derniers chiffres suffisent à reconnaître une ligne au support
// quand on a déjà le numéro sous les yeux, et ne permettent pas de le reconstituer.
func ExampleMasquerNumero() {
	fmt.Println(whatsapp.MasquerNumero("+24166123456"))
	// Output:
	// +241****56
}
