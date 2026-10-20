// Package whatsapp porte le vocabulaire du canal WhatsApp, et rien qui parle au réseau.
//
// Il décrit un message de gabarit approuvé, refuse ce que la plateforme refuse sans
// l'expliquer, fabrique les liens wa.me et classe les échecs d'envoi. Le dialecte d'une
// passerelle vit dans un sous-paquet, et c'est le seul endroit qui ouvre une connexion :
// aujourd'hui ayospush. Changer de fournisseur, y compris pour l'API Cloud de Meta en
// direct, ne doit toucher que ce sous-paquet.
//
// Il ne décide de rien d'applicatif : il ne journalise pas, ne lit pas l'environnement de
// lui-même, ne crée aucune goroutine, ne stocke rien, et ne convertit aucune erreur en code
// HTTP ni en code métier. Celui qui le câble garde ces politiques.
//
// # Deux mécanismes, et ils ne se confondent pas
//
// LE MESSAGE DE GABARIT part d'un serveur, par une passerelle payante. Hors de la fenêtre de
// service de vingt-quatre heures, c'est à dire dès qu'aucun message entrant récent du
// destinataire n'existe, WhatsApp n'accepte qu'un gabarit préapprouvé. C'est pourquoi l'envoi
// prend un [Modele] et jamais une chaîne : un port qui prendrait du texte libre laisserait
// croire qu'un message arbitraire peut partir, et le mensonge n'éclaterait qu'en production,
// au premier envoi.
//
// LE LIEN wa.me, lui, ne part de nulle part. [Lien] fabrique une adresse qui ouvre la
// discussion sur le téléphone de l'utilisateur, message déjà saisi, et c'est lui qui appuie
// sur envoyer. Aucun coût par message, aucun gabarit à faire approuver, et un geste qu'il
// garde la main de ne pas faire. Le texte y est libre, parce qu'il ne traverse aucune API.
//
// # Trois faits qu'on ne devine pas
//
// Ils coûtent une journée chacun quand on les apprend par l'erreur.
//
//   - LE NOM DU GABARIT N'EST PAS UNE CONSTANTE. La passerelle renomme les gabarits qu'on lui
//     soumet, en collant au nom demandé un suffixe de compte et d'horodatage de création, et
//     ce nom change à chaque recréation du gabarit. Il est donc une donnée de configuration,
//     sinon un gabarit recréé un matin coûte un déploiement. Voir [NouveauModele].
//   - LES VARIABLES SONT POSITIONNELLES, et leur ordre n'est pas celui d'une map. Mesuré : à
//     partir de dix variables, un objet JSON bâti depuis une map range « 10 » entre « 1 » et
//     « 2 », donc le message part avec les valeurs aux mauvaises places. [Modele] les garde
//     en tranche pour cette seule raison, et l'adaptateur sérialise en la parcourant.
//   - UN 2XX SANS IDENTIFIANT DE MESSAGE EST UN ÉCHEC. Sans cette clé, le message est
//     intraçable chez le fournisseur, donc ni réconciliable ni opposable au support, et un
//     succès rapporté sur la foi du seul code HTTP serait un succès qu'on ne peut pas
//     prouver. Voir [Reference].
//
// # Ce que l'appelant doit écrire lui-même
//
// Le plafond d'envoi. Les paliers d'un portefeuille WhatsApp se comptent en destinataires
// distincts par vingt-quatre heures, et ce paquet n'a aucun stockage : il ne peut donc pas
// tenir ce compte. Il rend [ErrQuotaAtteint], interrogeable par errors.Is sans avoir à
// connaître le nom du fournisseur, et c'est à l'appelant de décider ce que la saturation vaut
// pour lui. Rejouable, mais jamais tout de suite : l'attente utile est de l'ordre de l'heure.
package whatsapp
