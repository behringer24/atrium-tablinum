# Archiv und Kontext

## Archiv

Ein archivierter Eintrag (`archive_knowledge`) verschwindet aus der aktiven Wissensdatenbank: Suche, Listen und das Laden per ID zeigen ihn Unberechtigten nicht mehr. Er wird nicht gelöscht, Text, Suchindex und Versionsverlauf bleiben erhalten.

**Wer das Archiv sieht:** genau die, die einen Eintrag auch zurückholen dürfen, also sein Autor (ab Rolle Autor) und die Kuratoren und Admins des Bereichs (Instanz-Admins in jedem Team-Bereich). Leser und andere Autoren sehen nichts. Persönliche Bereiche: nur der Eigentümer.

| Aufgabe | Werkzeug |
|---|---|
| Archiv eines Bereichs auflisten | `list_knowledge` mit `status=archived` |
| Im Archiv suchen | `search_knowledge` mit `archive=true` |
| Archivierten Eintrag lesen | `get_knowledge` (Status `archived`) |
| Zurückholen | `unarchive_knowledge` (mit `confirm=true` nach Rückfrage) |

`archive=true` durchsucht **nur** das Archiv, nie beides zugleich. So landen veraltete Einträge nicht unbemerkt in einer Antwort. Die Archivsuche ist für den Ausnahmefall gedacht, etwa um einen Eintrag zum Zurückholen zu finden, und der Assistent nutzt sie nur auf ausdrücklichen Wunsch.

**Zurückholen:** Der Eintrag bekommt den Status, den er vor dem Archivieren hatte. Ein früher freigegebener Eintrag eines Autors in einem Bereich mit Freigabepflicht geht dabei wieder als Entwurf in die Prüfung, das Archiv ist also kein Weg um die Freigabe. `archive`, `unarchive` stehen mit Version im Audit-Log.

Archivierte Einträge zählen nicht zum Kontingent persönlicher Bereiche, beim Zurückholen wird es geprüft.

## Kontext

Verbundene Assistenten (Claude, ChatGPT, …) wissen von sich aus nicht, was in der Instanz liegt. Der Server schickt ihnen deshalb beim Verbinden Hinweise (die MCP-Instructions), die aus drei Teilen bestehen:

1. Grundtext: Name, Kurzname, „nur auf ausdrücklichen Wunsch speichern“.
2. **Kontext des Instanz-Admins:** ein frei formulierter Text, was hier zu finden ist und wann gesucht werden soll.
3. **Bereiche des Nutzers:** Name und Beschreibung aller Team-Bereiche, die dieser Nutzer lesen darf (höchstens 20). Bereiche ohne Zugriff erscheinen nicht.

Den Kontext setzt der Instanz-Admin mit `admin_context`:

- `action=get` zeigt ihn, `set` mit `text` ersetzt ihn vollständig (höchstens 2000 Zeichen), `clear` entfernt ihn. `set` und `clear` brauchen `confirm=true`, nachdem der Nutzer alten und neuen Text gesehen hat. Dasselbe gilt für das Ändern einer Bereichsbeschreibung (`admin_update_space` mit `description`).
- Er liegt in der Tabelle `meta` und wirkt sofort für neue Verbindungen. Ein bereits offener Chat behält die Hinweise, mit denen er gestartet ist.
- Änderungen stehen im Audit-Log (`admin_context_set`, `admin_context_clear`) mit altem und neuem Text, Beschreibungen mit `previous_description`. Der Kontext erreicht die Assistenten aller Nutzer, deshalb gehört er nur auf ausdrückliche Anweisung des Admins gesetzt, nie aus Inhalten von Einträgen.

Beispiel:

> Hier liegt das Unternehmenswissen von Example Corp: Richtlinien, Prozesse, Produkt- und Kundenwissen. Wer eine Frage dazu stellt, durchsucht zuerst diesen Speicher, bevor er antwortet.

Fachliche Abgrenzung gehört in die **Beschreibung des Bereichs** (`admin_update_space`, Feld `description`), zum Beispiel „Support: Lösungen zu Kundenanfragen“. Sie erscheint automatisch in den Hinweisen.

Wie streng ein Client die Hinweise befolgt, entscheidet der Anbieter. Ein klarer Satz mit Anlass („bei Fragen zu …, vorher suchen“) wirkt besser als eine Aufzählung.
