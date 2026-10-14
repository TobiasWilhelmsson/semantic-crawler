package semantic

import "golang.org/x/sys/unix"

func renameExportDirectory(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
