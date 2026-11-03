# Entwürfe und Freigabe

Ein Eintrag in einem Team-Bereich hat einen von vier Zuständen: `draft` (Entwurf), `approved` (freigegeben), `rejected` (zurückgegeben) oder `archived` (siehe [Archiv und Kontext](archive-and-context.md)). Lesen und durchsuchen können alle Mitglieder nur freigegebene Einträge. Entwürfe und zurückgegebene Einträge sehen nur ihr Autor und die Kuratoren des Bereichs. Persönliche Bereiche kennen keine Entwürfe.

## Wann ein Eintrag zum Entwurf wird

- **Freigabepflicht:** Hat der Bereich `require_approval`, sind Einträge von Autoren immer Entwürfe. Einträge von Kuratoren und Admins sind sofort freigegeben.
- **Auf ausdrücklichen Wunsch:** `store_knowledge` mit `draft=true` legt den Eintrag in jedem Team-Bereich als Entwurf an, auch ohne Freigabepflicht und auch für Admins. Das ist für den Fall gedacht, dass jemand für ein fremdes Fachgebiet schreibt und der Kurator des Bereichs den Inhalt vor der Veröffentlichung prüfen soll. Der Assistent setzt `draft` nur, wenn der Nutzer es ausdrücklich verlangt.
- **Verschieben:** Ein Eintrag, den ein Nicht-Kurator in einen Bereich mit Freigabepflicht verschiebt, wird dort zum Entwurf.

## Prüfen

Kuratoren und Admins eines Bereichs (Instanz-Admins in jedem Team-Bereich) sehen das Tool `review_knowledge`. Wartende Entwürfe findet `list_knowledge` mit `status=draft`.

1. Entwurf mit `get_knowledge` laden und lesen.
2. `review_knowledge` mit `decision=approve` gibt ihn frei. Das macht ihn für alle Leser des Bereichs sichtbar und braucht darum `confirm=true` nach Rückfrage beim Nutzer.
3. `decision=reject` gibt ihn mit einer Anmerkung (`note`, Pflicht) an den Autor zurück. Der Autor sieht sie als `review_note` am Eintrag.

Regeln:

- **Der Autor nur als Admin.** Ein Entwurf ist eine Bitte an den Bereich, einmal drüberzuschauen. Kuratoren können ihren eigenen Entwurf deshalb nicht selbst freigeben, das tut ein anderer Kurator. Admins des Bereichs und Instanz-Admins können es, damit nie etwas liegen bleibt, wenn Kollegen ausgefallen oder ausgeschieden sind. Im Audit-Log steht eine solche Freigabe mit `self`.
- Nur Entwürfe lassen sich prüfen. Freigegebene und zurückgegebene Einträge nicht.
- Ein zurückgegebener Eintrag geht mit der nächsten Änderung durch den Autor (`update_knowledge`) wieder als Entwurf in die Prüfung; die Anmerkung wird dabei gelöscht.
- Jede Prüfung erhöht die Version des Eintrags und steht mit `approve` oder `reject` im Audit-Log (ohne Anmerkung).
- Ein Kurator, der nicht Admin ist, bleibt mit seinem eigenen Entwurf auf einen anderen Kurator angewiesen; gibt es keinen, löst ein Admin den Knoten.

## Bekannte Grenzen

- Autoren eines Bereichs mit Freigabepflicht können freigegebene Einträge nicht selbst ändern (`update_knowledge` meldet das); ein Entwurf neben dem freigegebenen Stand ist noch nicht umgesetzt.
- Es gibt keine Benachrichtigung: Kuratoren finden Entwürfe nur über `list_knowledge`.
