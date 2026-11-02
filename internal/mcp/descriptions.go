package mcpserver

// defaultDescriptions are Go templates (.ProductName, .ShortName, .OrganizationName, .HandoffTTLDays)
// and can be replaced per tool via config tools.descriptions.
var defaultDescriptions = map[string]string{
	"list_spaces": `Listet die Wissensbereiche, auf die der Nutzer im Wissensspeicher „{{.ProductName}}“ von {{.OrganizationName}} Zugriff hat ` +
		`(Nutzer sagen meist kurz „{{.ShortName}}“), ` +
		`mit Rolle, Typ, Beschreibung und Freigabepflicht. Markiert den Standardbereich zum Speichern. ` +
		`Aufrufen, wenn unklar ist, wohin etwas gehört oder welche Bereiche es gibt.`,

	"store_knowledge": `Speichert Wissen dauerhaft im zentralen Wissensspeicher „{{.ProductName}}“ von {{.OrganizationName}}. ` +
		`NUR aufrufen, wenn der Nutzer ausdrücklich verlangt, etwas abzulegen, zu speichern oder zu dokumentieren ` +
		`(z. B. „Lege das zentral ab“, „Speichere das in {{.ShortName}}“, „Speichere das in meinem Bereich“, „Leg das im Bereich Support ab“). ` +
		`Niemals aus eigenem Antrieb aufrufen. Ohne Bereichsangabe wird im Standardbereich des Nutzers gespeichert ` +
		`(meist sein persönlicher Bereich) – nenne dem Nutzer danach, wo gespeichert wurde. ` +
		`Keine personenbezogenen Kundendaten, Passwörter oder Zugangsdaten speichern. ` +
		`Inhalt als eigenständig verständliches Markdown mit aussagekräftigem Titel formulieren. ` +
		`Liefert das Tool ähnliche Einträge oder eine Liste möglicher Bereiche zurück, wurde noch nichts gespeichert: ` +
		`den Nutzer fragen, ob der bestehende Eintrag aktualisiert werden soll bzw. welcher Bereich gemeint ist.`,

	"search_knowledge": `Durchsucht den Wissensspeicher „{{.ProductName}}“ (alle Bereiche, auf die der Nutzer Zugriff hat, inkl. seines persönlichen Bereichs). ` +
		`Aufrufen, wenn eine Frage firmenspezifisches oder vom Nutzer früher abgelegtes Wissen betreffen könnte ` +
		`oder der Nutzer „{{.ShortName}}“ erwähnt („Was steht in {{.ShortName}} zu …?“). ` +
		`Liefert Abschnitte mit Dokument-ID, Titel, Bereich, Autor, Datum und Status. ` +
		`Entwürfe als solche kennzeichnen; bei veralteten oder widersprüchlichen Treffern darauf hinweisen.`,

	"get_knowledge": `Lädt einen Eintrag des Wissensspeichers „{{.ProductName}}“ vollständig anhand seiner ID (aus einer Suche oder Liste).`,

	"update_knowledge": `Ändert einen bestehenden Eintrag im Wissensspeicher „{{.ProductName}}“. NUR auf ausdrückliche Anweisung des Nutzers aufrufen. ` +
		`Vorher mit get_knowledge den aktuellen Stand laden und den vollständigen neuen Inhalt übergeben (nicht nur die Änderung). ` +
		`Nicht übergebene Felder bleiben unverändert.`,

	"archive_knowledge": `Archiviert einen Eintrag im Wissensspeicher „{{.ProductName}}“, er ist danach nicht mehr les- und suchbar. ` +
		`NUR auf ausdrückliche Anweisung. Vorher den Nutzer fragen und erst nach seiner Bestätigung mit confirm=true aufrufen.`,

	"move_knowledge": `Verschiebt einen freigegebenen Eintrag des Wissensspeichers „{{.ProductName}}“ in einen anderen Bereich. ` +
		`NUR auf ausdrückliche Anweisung. Ziel kann nur ein Bereich sein, in dem der Nutzer schreiben darf; ` +
		`verschieben darf, wer den Eintrag auch ändern darf. In Bereichen mit Freigabepflicht wird der Eintrag zum Entwurf. ` +
		`Vorher den Nutzer fragen und erst nach seiner Bestätigung mit confirm=true aufrufen. ` +
		`Liefert das Tool eine Liste möglicher Bereiche zurück, wurde nichts verschoben: den Nutzer fragen, welcher gemeint ist.`,

	"delete_knowledge": `Löscht einen Eintrag des Wissensspeichers „{{.ProductName}}“ endgültig samt Versionsverlauf; das lässt sich nicht rückgängig machen. ` +
		`Nur Admins des Bereichs (und Instanz-Admins) dürfen das, im persönlichen Bereich der Eigentümer. ` +
		`NUR auf ausdrückliche Anweisung; zum bloßen Ausblenden stattdessen archive_knowledge verwenden. ` +
		`Vorher den Nutzer fragen und erst nach seiner Bestätigung mit confirm=true aufrufen.`,

	"list_knowledge": `Listet die neuesten Einträge eines Bereichs im Wissensspeicher „{{.ProductName}}“, optional gefiltert nach Tag. ` +
		`Ohne Bereichsangabe wird der persönliche Bereich des Nutzers gelistet.`,

	"set_default_space": `Legt fest, in welchem Bereich Wissen standardmäßig gespeichert wird, wenn der Nutzer keinen nennt. ` +
		`NUR auf ausdrückliche Anweisung. Ohne Bereichsangabe wird die Einstellung zurückgesetzt (dann gilt der persönliche Bereich).`,

	"handoff_save": `Legt eine Übergabe im persönlichen Bereich des Nutzers ab, damit er in einem anderen KI-Assistenten ` +
		`(z. B. ChatGPT, Claude) weiterarbeiten kann. NUR auf ausdrückliche Anweisung („Leg den Stand für ChatGPT ab“, „Übergabe speichern“). ` +
		`Fasse den relevanten Stand eigenständig verständlich zusammen: Ziel, bisherige Ergebnisse, offene Punkte, nächste Schritte. ` +
		`Übergaben laufen automatisch nach {{.HandoffTTLDays}} Tagen ab.`,

	"handoff_list": `Listet die eigenen, nicht abgelaufenen Übergaben des Nutzers, neueste zuerst.`,

	"handoff_load": `Lädt eine Übergabe des Nutzers per ID oder, ohne ID, die neueste („Lade meine letzte Übergabe“). ` +
		`Nach dem Laden den Stand zusammenfassen und mit den nächsten Schritten fortfahren.`,

	"space_members": `Zeigt Mitglieder und Rollen eines Bereichs, den der Nutzer als Bereichs-Admin verwaltet, ` +
		`samt Freigabepflicht und Standardrolle für alle Angemeldeten.`,

	"space_add_member": `Fügt einem Bereich, den der Nutzer als Bereichs-Admin verwaltet, einen Nutzer (user_email) oder eine Gruppe (group) ` +
		`mit einer Rolle hinzu oder ändert deren Rolle: reader (lesen), author (speichern), curator (freigeben), admin (verwalten). ` +
		`NUR auf ausdrückliche Anweisung. Nutzer müssen sich vorher mindestens einmal angemeldet haben.`,

	"space_remove_member": `Entfernt einen Nutzer oder eine Gruppe aus einem verwalteten Bereich. NUR auf ausdrückliche Anweisung; ` +
		`vorher den Nutzer fragen und erst nach Bestätigung mit confirm=true aufrufen.`,

	"admin_create_space": `Legt einen neuen Team-Bereich im Wissensspeicher „{{.ProductName}}“ an (Instanz-Admin). ` +
		`Optional mit Freigabepflicht, Standardrolle für alle Angemeldeten (reader/author) und ersten Mitgliedern. NUR auf ausdrückliche Anweisung.`,

	"admin_update_space": `Ändert die Einstellungen eines bestehenden Team-Bereichs (Instanz-Admin): Anzeigename, Beschreibung, ` +
		`Freigabepflicht und Standardrolle für alle Angemeldeten (reader/author, none = nur Mitglieder). ` +
		`Nicht übergebene Felder bleiben unverändert; bestehende Einträge behalten ihren Status. NUR auf ausdrückliche Anweisung.`,

	"admin_archive_space": `Archiviert einen Team-Bereich (Instanz-Admin): er ist danach für Nutzer nicht mehr sichtbar, die Inhalte bleiben erhalten ` +
		`und lassen sich mit admin_restore_space zurückholen. Vorher den Nutzer fragen, dann mit confirm=true aufrufen.`,

	"admin_restore_space": `Stellt einen archivierten Team-Bereich wieder her (Instanz-Admin).`,

	"admin_list_users": `Listet alle Nutzer mit Status, letzter Anmeldung und – von persönlichen Bereichen – nur Anzahl, Größe und letzter Änderung (Instanz-Admin). ` +
		`Inhalte persönlicher Bereiche sind für niemanden einsehbar.`,

	"admin_offboard_user": `Offboarding eines ausscheidenden Nutzers (Instanz-Admin): sperrt das Konto sofort, widerruft alle Tokens und löscht seine Übergaben. ` +
		`Sein persönlicher Bereich bleibt zunächst unangetastet und privat; er kann danach mit admin_transfer_personal_space übertragen werden ` +
		`und wird sonst nach Ablauf der Aufbewahrungsfrist gelöscht. NUR auf ausdrückliche Anweisung. ` +
		`Vorher den Nutzer fragen und erst nach seiner Bestätigung mit confirm=true aufrufen.`,

	"admin_transfer_personal_space": `Überträgt den persönlichen Bereich eines gesperrten Nutzers (Instanz-Admin, nur nach admin_offboard_user). ` +
		`mode=space wandelt ihn in einen Team-Bereich um (slug, name, members – z. B. Vorgesetzter als admin, Nachfolger als curator, ` +
		`damit sie Einträge sichten, verschieben oder löschen können); Instanz-Admins sehen ihn dann wie jeden Team-Bereich. ` +
		`mode=merge verschiebt alle Einträge in den persönlichen Bereich von target_email (dessen Kontingent gilt). ` +
		`Einträge, IDs und Versionsverlauf bleiben erhalten. NUR auf ausdrückliche Anweisung; die Inhalte werden damit für andere lesbar. ` +
		`Vorher den Nutzer fragen und erst nach seiner Bestätigung mit confirm=true aufrufen.`,

	"admin_reactivate_user": `Hebt die Sperre eines Nutzers auf (Instanz-Admin), etwa nach einem versehentlichen Offboarding; ` +
		`die Löschfrist seines persönlichen Bereichs endet damit. Widerrufene Tokens bleiben ungültig, der Nutzer meldet sich neu an. ` +
		`NUR auf ausdrückliche Anweisung.`,

	"admin_groups": `Verwaltet lokale Gruppen (Instanz-Admin): list, create, rename, delete, add_member, remove_member. ` +
		`Gruppen aus dem Identity Provider sind schreibgeschützt. Zum Löschen vorher den Nutzer fragen und confirm=true setzen.`,

	"admin_audit_log": `Fragt das Audit-Log ab (Instanz-Admin), filterbar nach Nutzer, Aktion, Bereich, Eintrag und Zeitraum. ` +
		`Aktionen in persönlichen Bereichen sind nur mit Zeitpunkt und Aktion enthalten.`,

	"admin_health": `Zeigt den Systemzustand (Instanz-Admin): Datenbank, Embedding-Dienst, Hintergrundaufgaben und Jobs.`,
}
