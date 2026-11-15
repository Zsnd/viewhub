package gb

import "database/sql"

// DeleteDevice removes the inventory and its wall references in one transaction.
// The space predicate deliberately treats foreign devices as missing.
func (s *Store) DeleteDevice(space int64, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("DELETE FROM devices WHERE space_id=? AND id=?", space, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	_, err = tx.Exec(`UPDATE walls SET data=json_set(data,'$.slots',json((
	 SELECT json_group_array(CASE WHEN substr(value,1,length(?))=? THEN '' ELSE value END ORDER BY key)
	 FROM json_each(walls.data,'$.slots')
	))) WHERE space_id=?`, "gb/"+id+"/", "gb/"+id+"/", space)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (g *Gateway) DeleteDevice(space int64, id string) error {
	g.mu.Lock()
	if err := g.Store.DeleteDevice(space, id); err != nil {
		g.mu.Unlock()
		return err
	}
	delete(g.online, id)
	delete(g.attempts, id)
	delete(g.catalogs, id)
	talk := g.talks[id]
	g.mu.Unlock()
	if talk != nil {
		talk.Close()
	}
	return nil
}
