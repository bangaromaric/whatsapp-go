# whatsapp-go

Client Go du canal **WhatsApp Business**, écrit pour être réutilisé tel quel dans un autre
projet. Le vocabulaire du canal vit à la racine, le dialecte d'une passerelle dans un
sous-paquet : aujourd'hui **AyosPush**.

Zéro dépendance hors bibliothèque standard dans le code de production. `go-cmp` est une
dépendance de test, et l'élagage de graphe de Go la garde hors du build de qui importe ce
module.

```bash
go get github.com/bangaromaric/whatsapp-go
```

## Les deux mécanismes, et ils ne se confondent pas

C'est la distinction qui décide de tout le reste, et elle n'est pas intuitive.

| | Message de gabarit | Lien `wa.me` |
|---|---|---|
| Part de | votre serveur, par la passerelle | le téléphone de l'utilisateur |
| Coût | facturé par message | aucun |
| Texte | gabarit préapprouvé, variables positionnelles | libre |
| Qui appuie sur envoyer | personne, c'est une API | l'utilisateur, et il garde la main de ne pas le faire |
| Dans ce module | `ayospush.Client.Envoyer` avec un `Modele` | `whatsapp.Lien` |

Hors de la **fenêtre de service de vingt-quatre heures**, c'est à dire dès qu'aucun message
entrant récent du destinataire n'existe, WhatsApp n'accepte qu'un gabarit préapprouvé. C'est
pourquoi l'envoi prend un `Modele` et jamais une chaîne : un port qui prendrait du texte
libre laisserait croire qu'un message arbitraire peut partir, et le mensonge n'éclaterait
qu'en production, au premier envoi.

## Ce que le module fait, et ce qu'il laisse à l'appelant

| Il fait | Il ne fait pas |
|---|---|
| Le jeton de la passerelle, en cache, sous verrou, renouvelé proactivement et réactivement | Lancer une goroutine. La connexion est paresseuse, et le verrou est tenu pendant |
| L'envoi d'un message de gabarit, et la référence du message accepté | Compter le plafond. Les paliers se comptent en destinataires par 24 h, et ce module n'a aucun stockage |
| Refuser avant la moindre requête ce que la plateforme refuse en silence : variable vide, saut de ligne, UTF-8 invalide, variable trop longue | Vérifier l'arité du gabarit. Seul Meta connaît le gabarit ; cela se vérifie une fois, à la main |
| Classer les échecs derrière six sentinelles, dont celles qui disent « rejouable » | Journaliser. Il rend des erreurs, l'appelant a sa politique de trace. `MasquerNumero` est offerte pour cela |
| Masquer un numéro pour un journal, et fournir un bouchon qui n'envoie rien | Connaître la notion d'environnement. Refuser le bouchon en production est une politique d'appelant |
| Fabriquer les liens `wa.me`, encodage compris | Lire l'environnement de lui-même. `Reglages` est une valeur, pas une variable |
| | Traduire en codes HTTP ni en codes métier. C'est au projet appelant de le faire, en un seul endroit |

## Les secrets à réunir, et où ils se trouvent

| Quoi | Où |
|---|---|
| `BaseURL` | l'hôte de la passerelle. Confirmé par envoi réel le 8 octobre 2026 : `https://ayospush.com` |
| `CleAPI` et `SecretAPI` | le compte applicatif chez le fournisseur |
| `Expediteur` | le `phone_number_id` du numéro WhatsApp Business émetteur. Semi-public chez Meta, à traiter comme un secret : il identifie le compte |
| le nom du gabarit | **pas un secret, et surtout pas une constante.** Voir plus bas |

## Démarrer

```go
client, err := ayospush.Nouveau(ayospush.Reglages{
    BaseURL:    "https://ayospush.com",
    CleAPI:     os.Getenv("WHATSAPP_API_KEY"),
    SecretAPI:  os.Getenv("WHATSAPP_API_SECRET"),
    Expediteur: os.Getenv("WHATSAPP_SENDER_ID"),
})
if err != nil {
    return fmt.Errorf("passerelle whatsapp: %w", err) // aucune requete n'a ete emise
}

modele, err := whatsapp.NouveauModele(nomDuGabarit, "123456")
if err != nil {
    return err
}

reference, err := client.Envoyer(ctx, "+24166123456", modele)
```

`Nouveau` valide les quatre réglages et **n'émet aucune requête** : se connecter pour
découvrir qu'une clé est vide enverrait des identifiants vides sur le réseau, et le journal
du fournisseur en garderait la trace. C'est aussi ce qui permet au programme de refuser de
démarrer plutôt que d'échouer au premier message.

### Le port à déclarer chez vous

Déclarez l'interface côté consommateur, et ce module n'apparaîtra pas dans votre couche
applicative :

```go
type passerelle interface {
    Envoyer(ctx context.Context, numeroE164 string, m whatsapp.Modele) (whatsapp.Reference, error)
}
```

`*ayospush.Client` et `whatsapp.Bouchon` la satisfont tous les deux, sans rien déclarer.

## Trois faits qu'on ne devine pas

Ils coûtent une journée chacun quand on les apprend par l'erreur.

1. **Le nom du gabarit n'est pas une constante.** La passerelle **renomme** les gabarits
   qu'on lui soumet, en collant au nom demandé un suffixe de compte et d'horodatage de
   création : `mon_gabarit` devient `mon_gabarit_biz1_1013165939`. Et ce nom change à chaque
   recréation du gabarit, mesuré deux fois le même jour. Il est donc une donnée de
   configuration ; une constante compilée coûterait un déploiement.
2. **Les variables sont positionnelles, et leur ordre n'est pas celui d'une map.** À partir
   de dix variables, un objet JSON bâti depuis une map range `"10"` entre `"1"` et `"2"` :
   le message part, facturé, avec les valeurs aux mauvaises places. `Modele` les garde en
   tranche pour cette seule raison, et l'adaptateur sérialise en la parcourant.
3. **Un 2xx sans identifiant de message est un échec.** Sans cette clé, le message est
   intraçable chez le fournisseur, donc ni réconciliable ni opposable au support. Le module
   rend alors `ayospush.ErrSansReference` malgré le code de succès.

## Deux particularités du contrat de la passerelle

Première cause d'échec au portage, et elles sont contre-intuitives.

- **La connexion est en JSON, l'envoi est en multipart.** Le type de contenu se vérifie donc
  par point d'entrée et non par fournisseur.
- **Le champ `variables` n'est pas un objet JSON dans un corps JSON** : c'est une *chaîne*
  contenant du JSON, posée dans un champ de formulaire.

## Traiter les erreurs

Le discriminant est le motif, pas le code HTTP.

```go
switch {
case errors.Is(err, whatsapp.ErrQuotaAtteint):
    // Rejouable, mais JAMAIS tout de suite : le palier se compte en destinataires
    // distincts par vingt-quatre heures, donc l'attente utile est de l'ordre de l'heure.
case errors.Is(err, ayospush.ErrRequeteRefusee):
    // Gabarit inconnu, arite fausse, numero invalide. NE JAMAIS REJOUER : un rejeu
    // echouerait a l'identique et ferait d'une faute de configuration une tempete.
case errors.Is(err, ayospush.ErrAuthentification):
    // Identifiants refuses, ou jeton refuse deux fois de suite.
case errors.Is(err, ayospush.ErrSansReference):
    // 2xx, mais message intracable. A traiter comme un echec.
case errors.Is(err, ayospush.ErrIndisponible):
    // Panne ou delai depasse. Rejouable.
case errors.Is(err, ayospush.ErrConfiguration):
    // Reglage absent. Aucune requete n'a ete emise.
}
```

**`whatsapp.ErrQuotaAtteint` vit à la racine et non dans le paquet du fournisseur**, et c'est
ce qui la rend utilisable : un appelant qui doit en tirer une phrase à l'écran n'a pas à
importer `ayospush`, donc pas à apprendre le nom du fournisseur pour écrire une phrase en
français. `ayospush.ErrQuota` l'emballe, et les deux restent interrogeables sur la même
erreur.

Tout échec d'envoi arrive emballé dans un `whatsapp.EnvoiError`, qui **ne porte aucune
catégorie applicative**, délibérément : un échec de transport n'est pas une règle violée par
l'utilisateur, et c'est à l'appelant, qui sait pourquoi il envoyait, de décider ce que
l'échec vaut pour lui.

## Le bouchon

`whatsapp.NouveauBouchon(journal)` rend une implémentation qui journalise et n'envoie rien.
Elle sert au développement et à la démonstration : un poste de travail n'a ni identifiants
ni gabarit approuvé, et un envoi réel depuis une boucle de développement se paie en messages
facturés chez de vraies personnes. Les références qu'il rend portent le préfixe `bouchon-`,
pour qu'une ligne jamais partie se reconnaisse d'un coup d'œil en base.

**Un bouchon rend TOUJOURS un succès.** Là où WhatsApp est le canal unique, un bouchon laissé
actif en production ferait paraître le service fonctionnel alors que personne ne recevrait
jamais rien. La parade n'est pas dans ce module, qui ignore la notion d'environnement : c'est
à l'appelant de refuser de démarrer en production quand l'envoi réel n'est pas activé, et non
l'inverse.

## Deux réserves assumées

**Aucun accusé de livraison.** La réponse d'un envoi vaut « accepté pour émission » et jamais
« remis ». Le champ `status` de la passerelle n'est volontairement pas lu : le lire inviterait
à en tirer une conclusion sur la livraison, que seul un accusé de réception pourrait donner.
La `Reference` est ce qui permettra de brancher cet accusé le jour où un webhook existera.

**L'arité d'un gabarit ne se vérifie pas ici.** Seul Meta connaît le gabarit. Une arité fausse
se découvre au `curl` de mise en service, une fois, et le module ne peut refuser que ce qui
est refusable sans le gabarit : variable vide, saut de ligne, tabulation, UTF-8 invalide,
longueur au-delà de cent vingt caractères.

## Vérifier

```bash
bash verifier.sh
```

Format, vet, modernizer, tests sous détecteur de course, exemples, **absence de dépendance
tierce en production**, `go mod tidy`, et govulncheck s'il est installé.
