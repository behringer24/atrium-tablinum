# Offboarding

Persönliche Bereiche sind nur für ihren Eigentümer sichtbar, auch für Instanz-Admins nicht. Scheidet jemand aus, entscheidet ein Instanz-Admin, was mit dem Wissen darin passiert. Alle Schritte laufen über MCP-Werkzeuge und stehen im Audit-Log, dort nur mit Konto und Anzahl, nie mit Titeln oder Inhalten.

## Ablauf

1. **Vor dem letzten Arbeitstag:** Der Mitarbeiter verschiebt selbst mit `move_knowledge`, was ins Team gehört. Das ist der sauberste Weg, weil er selbst entscheidet.
2. **Sperren:** `admin_offboard_user` sperrt das Konto sofort, widerruft alle OAuth-Tokens und löscht die Übergaben. Der persönliche Bereich bleibt privat; zurück kommen nur Anzahl, Größe und Löschdatum (`purge_after`).
3. **Übertragen:** `admin_transfer_personal_space` geht nur für gesperrte Konten.
   - `mode=space` wandelt den persönlichen Bereich in einen Team-Bereich um, z. B. „Nachlass Max Muster“ mit dem Vorgesetzten als `admin` und dem Nachfolger als `curator`. Einträge, IDs und Versionsverlauf bleiben erhalten. Die Mitglieder sichten, verschieben (auch in ihren eigenen persönlichen Bereich) oder löschen; danach wird der Bereich mit `admin_archive_space` archiviert. Der Nachfolger braucht `curator`, weil Autoren nur eigene Einträge ändern und verschieben dürfen. Wie jeder Team-Bereich ist er für Instanz-Admins sichtbar.
   - `mode=merge` verschiebt alle Einträge in den persönlichen Bereich des Nachfolgers, der sie danach wie eigene besitzt. Sein Kontingent gilt; reicht es nicht, wird nichts verschoben.
4. **Frist:** Persönliche Bereiche gesperrter Konten werden nach `spaces.personal.retention_days_after_offboarding` Tagen (Standard 30) samt Inhalt gelöscht, sofern sie nicht umgewandelt wurden. Ein Hintergrundjob prüft das stündlich; `0` schaltet das Löschen ab.

`admin_reactivate_user` hebt eine Sperre wieder auf, etwa nach einem Versehen, und beendet die Frist. Wurde der Bereich schon umgewandelt oder gelöscht, entsteht beim nächsten Login ein neuer, leerer. Das Konto selbst wird nie gelöscht: Seine Einträge in Team-Bereichen behalten ihren Autor.

## Datenschutz

Vor dem Livegang festlegen und den Mitarbeitern mitteilen, dass der persönliche Bereich nur für dienstliches Wissen gedacht ist und beim Ausscheiden übertragen werden kann. Eine Übertragung gegen den Willen des Mitarbeiters nur dokumentiert und möglichst im Vier-Augen-Prinzip vornehmen; das Verfahren mit dem Datenschutzbeauftragten und gegebenenfalls dem Betriebsrat abstimmen.

Die Trennung gilt in der Anwendung. Wer direkten Zugriff auf Datenbank oder Backups hat, kann technisch alles lesen.
