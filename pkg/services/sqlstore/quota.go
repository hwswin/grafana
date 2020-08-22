package sqlstore

import (
	"fmt"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/setting"
)

func (ss *SqlStore) addQuotaHandlers() {
	bus.AddHandler("sql", ss.GetOrgQuotaByTarget)
	bus.AddHandler("sql", ss.GetOrgQuotas)
	bus.AddHandler("sql", ss.UpdateOrgQuota)
	bus.AddHandler("sql", ss.GetUserQuotaByTarget)
	bus.AddHandler("sql", ss.GetUserQuotas)
	bus.AddHandler("sql", ss.UpdateUserQuota)
	bus.AddHandler("sql", ss.GetGlobalQuotaByTarget)
}

type targetCount struct {
	Count int64
}

func (ss *SqlStore) GetOrgQuotaByTarget(query *models.GetOrgQuotaByTargetQuery) error {
	quota := models.Quota{
		Target: query.Target,
		OrgId:  query.OrgId,
	}
	has, err := ss.engine.Get(&quota)
	if err != nil {
		return err
	}
	if !has {
		quota.Limit = query.Default
	}

	//get quota used.
	rawSql := fmt.Sprintf("SELECT COUNT(*) as count from %s where org_id=?", ss.Dialect.Quote(query.Target))
	resp := make([]*targetCount, 0)
	if err := ss.engine.SQL(rawSql, query.OrgId).Find(&resp); err != nil {
		return err
	}

	query.Result = &models.OrgQuotaDTO{
		Target: query.Target,
		Limit:  quota.Limit,
		OrgId:  query.OrgId,
		Used:   resp[0].Count,
	}

	return nil
}

func (ss *SqlStore) GetOrgQuotas(query *models.GetOrgQuotasQuery) error {
	quotas := make([]*models.Quota, 0)
	sess := ss.engine.Table("quota")
	if err := sess.Where("org_id=? AND user_id=0", query.OrgId).Find(&quotas); err != nil {
		return err
	}

	defaultQuotas := setting.Quota.Org.ToMap()

	seenTargets := make(map[string]bool)
	for _, q := range quotas {
		seenTargets[q.Target] = true
	}

	for t, v := range defaultQuotas {
		if _, ok := seenTargets[t]; !ok {
			quotas = append(quotas, &models.Quota{
				OrgId:  query.OrgId,
				Target: t,
				Limit:  v,
			})
		}
	}

	result := make([]*models.OrgQuotaDTO, len(quotas))
	for i, q := range quotas {
		//get quota used.
		rawSql := fmt.Sprintf("SELECT COUNT(*) as count from %s where org_id=?", ss.Dialect.Quote(q.Target))
		resp := make([]*targetCount, 0)
		if err := ss.engine.SQL(rawSql, q.OrgId).Find(&resp); err != nil {
			return err
		}
		result[i] = &models.OrgQuotaDTO{
			Target: q.Target,
			Limit:  q.Limit,
			OrgId:  q.OrgId,
			Used:   resp[0].Count,
		}
	}
	query.Result = result
	return nil
}

func (ss *SqlStore) UpdateOrgQuota(cmd *models.UpdateOrgQuotaCmd) error {
	return ss.inTransaction(func(sess *DBSession) error {
		//Check if quota is already defined in the DB
		quota := models.Quota{
			Target: cmd.Target,
			OrgId:  cmd.OrgId,
		}
		has, err := sess.Get(&quota)
		if err != nil {
			return err
		}
		quota.Updated = time.Now()
		quota.Limit = cmd.Limit
		if !has {
			quota.Created = time.Now()
			//No quota in the DB for this target, so create a new one.
			if _, err := sess.Insert(&quota); err != nil {
				return err
			}
		} else {
			//update existing quota entry in the DB.
			_, err := sess.ID(quota.Id).Update(&quota)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (ss *SqlStore) GetUserQuotaByTarget(query *models.GetUserQuotaByTargetQuery) error {
	quota := models.Quota{
		Target: query.Target,
		UserId: query.UserId,
	}
	has, err := ss.engine.Get(&quota)
	if err != nil {
		return err
	} else if !has {
		quota.Limit = query.Default
	}

	//get quota used.
	rawSql := fmt.Sprintf("SELECT COUNT(*) as count from %s where user_id=?", ss.Dialect.Quote(query.Target))
	resp := make([]*targetCount, 0)
	if err := ss.engine.SQL(rawSql, query.UserId).Find(&resp); err != nil {
		return err
	}

	query.Result = &models.UserQuotaDTO{
		Target: query.Target,
		Limit:  quota.Limit,
		UserId: query.UserId,
		Used:   resp[0].Count,
	}

	return nil
}

func (ss *SqlStore) GetUserQuotas(query *models.GetUserQuotasQuery) error {
	quotas := make([]*models.Quota, 0)
	sess := ss.engine.Table("quota")
	if err := sess.Where("user_id=? AND org_id=0", query.UserId).Find(&quotas); err != nil {
		return err
	}

	defaultQuotas := setting.Quota.User.ToMap()

	seenTargets := make(map[string]bool)
	for _, q := range quotas {
		seenTargets[q.Target] = true
	}

	for t, v := range defaultQuotas {
		if _, ok := seenTargets[t]; !ok {
			quotas = append(quotas, &models.Quota{
				UserId: query.UserId,
				Target: t,
				Limit:  v,
			})
		}
	}

	result := make([]*models.UserQuotaDTO, len(quotas))
	for i, q := range quotas {
		//get quota used.
		rawSql := fmt.Sprintf("SELECT COUNT(*) as count from %s where user_id=?", ss.Dialect.Quote(q.Target))
		resp := make([]*targetCount, 0)
		if err := ss.engine.SQL(rawSql, q.UserId).Find(&resp); err != nil {
			return err
		}
		result[i] = &models.UserQuotaDTO{
			Target: q.Target,
			Limit:  q.Limit,
			UserId: q.UserId,
			Used:   resp[0].Count,
		}
	}
	query.Result = result
	return nil
}

func (ss *SqlStore) UpdateUserQuota(cmd *models.UpdateUserQuotaCmd) error {
	return ss.inTransaction(func(sess *DBSession) error {
		//Check if quota is already defined in the DB
		quota := models.Quota{
			Target: cmd.Target,
			UserId: cmd.UserId,
		}
		has, err := sess.Get(&quota)
		if err != nil {
			return err
		}
		quota.Updated = time.Now()
		quota.Limit = cmd.Limit
		if !has {
			quota.Created = time.Now()
			//No quota in the DB for this target, so create a new one.
			if _, err := sess.Insert(&quota); err != nil {
				return err
			}
		} else {
			//update existing quota entry in the DB.
			_, err := sess.ID(quota.Id).Update(&quota)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (ss *SqlStore) GetGlobalQuotaByTarget(query *models.GetGlobalQuotaByTargetQuery) error {
	//get quota used.
	rawSql := fmt.Sprintf("SELECT COUNT(*) as count from %s", ss.Dialect.Quote(query.Target))
	resp := make([]*targetCount, 0)
	if err := ss.engine.SQL(rawSql).Find(&resp); err != nil {
		return err
	}

	query.Result = &models.GlobalQuotaDTO{
		Target: query.Target,
		Limit:  query.Default,
		Used:   resp[0].Count,
	}

	return nil
}
