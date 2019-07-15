// Copyright 2019 Yunion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"context"

	"github.com/pkg/errors"

	"yunion.io/x/jsonutils"
	"yunion.io/x/log"
	"yunion.io/x/sqlchemy"

	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/cloudcommon/validators"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
)

type SLoadbalancerClusterManager struct {
	db.SStandaloneResourceBaseManager
}

var LoadbalancerClusterManager *SLoadbalancerClusterManager

func init() {
	LoadbalancerClusterManager = &SLoadbalancerClusterManager{
		SStandaloneResourceBaseManager: db.NewStandaloneResourceBaseManager(
			SLoadbalancerCluster{},
			"loadbalancerclusters_tbl",
			"loadbalancercluster",
			"loadbalancerclusters",
		),
	}
	LoadbalancerClusterManager.SetVirtualObject(LoadbalancerClusterManager)
}

type SLoadbalancerCluster struct {
	db.SStandaloneResourceBase
	SZoneResourceBase
}

func (man *SLoadbalancerClusterManager) ValidateCreateData(ctx context.Context, userCred mcclient.TokenCredential, ownerId mcclient.IIdentityProvider, query jsonutils.JSONObject, data *jsonutils.JSONDict) (*jsonutils.JSONDict, error) {
	zoneV := validators.NewModelIdOrNameValidator("zone", "zone", ownerId)
	if err := zoneV.Validate(data); err != nil {
		return nil, err
	}
	return man.SStandaloneResourceBaseManager.ValidateCreateData(ctx, userCred, ownerId, query, data)
}

func (lbc *SLoadbalancerCluster) ValidateDeleteCondition(ctx context.Context) error {
	men := []db.IModelManager{
		LoadbalancerManager,
	}
	lbcId := lbc.Id
	for _, man := range men {
		t := man.TableSpec().Instance()
		pdF := t.Field("pending_deleted")
		n, err := t.Query().
			Equals("cluster_id", lbcId).
			Filter(sqlchemy.OR(sqlchemy.IsNull(pdF), sqlchemy.IsFalse(pdF))).
			CountWithError()
		if err != nil {
			return httperrors.NewInternalServerError("get lbcluster refcount fail %v", err)
		}
		if n > 0 {
			return httperrors.NewResourceBusyError("lbcluster %s(%s) is still referred to by %d %s",
				lbcId, lbc.Name, n, man.KeywordPlural())
		}
	}
	return lbc.SStandaloneResourceBase.ValidateDeleteCondition(ctx)
}

func (lbc *SLoadbalancerCluster) GetCustomizeColumns(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject) *jsonutils.JSONDict {
	extra := lbc.SStandaloneResourceBase.GetCustomizeColumns(ctx, userCred, query)
	zoneInfo := lbc.SZoneResourceBase.GetCustomizeColumns(ctx, userCred, query)
	if zoneInfo != nil {
		extra.Update(zoneInfo)
	}
	return extra
}

func (lbc *SLoadbalancerCluster) GetExtraDetails(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject) (*jsonutils.JSONDict, error) {
	extra := lbc.GetCustomizeColumns(ctx, userCred, query)
	return extra, nil
}

func (lbc *SLoadbalancerCluster) CustomizeDelete(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) error {
	lbagents := []SLoadbalancerAgent{}
	q := LoadbalancerAgentManager.Query().Equals("cluster_id", lbc.Id)
	if err := db.FetchModelObjects(LoadbalancerAgentManager, q, &lbagents); err != nil {
		return errors.WithMessagef(err, "lbcluster %s(%s): find lbagents", lbc.Name, lbc.Id)
	}
	for i := range lbagents {
		lbagent := &lbagents[i]
		if err := lbagent.ValidateDeleteCondition(ctx); err != nil {
			return errors.WithMessagef(err, "lbagent %s(%s): validate delete", lbagent.Name, lbagent.Id)
		}
		if err := lbagent.CustomizeDelete(ctx, userCred, query, data); err != nil {
			return errors.WithMessagef(err, "lbagent %s(%s): customize delete", lbagent.Name, lbagent.Id)
		}
		lbagent.PreDelete(ctx, userCred)
		if err := lbagent.Delete(ctx, userCred); err != nil {
			return errors.WithMessagef(err, "lbagent %s(%s): delete", lbagent.Name, lbagent.Id)
		}
		lbagent.PostDelete(ctx, userCred)
	}
	return nil
}

func (lbc *SLoadbalancerCluster) Delete(ctx context.Context, userCred mcclient.TokenCredential) error {
	return nil
}

func (man *SLoadbalancerClusterManager) findByZoneId(zoneId string) []SLoadbalancerCluster {
	r := []SLoadbalancerCluster{}
	q := man.Query().Equals("zone_id", zoneId)
	if err := db.FetchModelObjects(man, q, &r); err != nil {
		log.Errorf("find lbclusters by zone_id %s: %v", zoneId, err)
		return nil
	}
	return r
}

func (man *SLoadbalancerClusterManager) InitializeData() error {
	// find existing lb with empty clusterid
	lbs := []SLoadbalancer{}
	lbQ := LoadbalancerManager.Query().
		IsFalse("pending_deleted").
		IsNullOrEmpty("manager_id").
		IsNullOrEmpty("cluster_id")
	if err := db.FetchModelObjects(LoadbalancerManager, lbQ, &lbs); err != nil {
		return errors.WithMessage(err, "find lb with empty cluster_id")
	}

	// create 1 cluster for each zone
	zoneCluster := map[string]*SLoadbalancerCluster{}
	for i := range lbs {
		lb := &lbs[i]
		zoneId := lb.ZoneId
		if zoneId == "" {
			// just in case
			log.Warningf("found lb with empty zone_id: %s(%s)", lb.Name, lb.Id)
			continue
		}
		lbc, ok := zoneCluster[zoneId]
		if !ok {
			lbcs := man.findByZoneId(zoneId)
			if len(lbcs) == 0 {
				m, err := db.NewModelObject(man)
				if err != nil {
					return errors.WithMessage(err, "new model object")
				}
				lbc = m.(*SLoadbalancerCluster)
				lbc.Name = "auto-lbc-" + zoneId
				lbc.ZoneId = zoneId
				if err := man.TableSpec().Insert(lbc); err != nil {
					return errors.WithMessage(err, "insert lbcluster model")
				}
			} else {
				if len(lbcs) > 1 {
					log.Infof("zone %s has %d lbclusters, select one", zoneId, len(lbcs))
				}
				lbc = &lbcs[0]
			}
			zoneCluster[zoneId] = lbc
		}
		if _, err := db.UpdateWithLock(context.Background(), lb, func() error {
			lb.ClusterId = lbc.Id
			return nil
		}); err != nil {
			return errors.WithMessagef(err, "lb %s(%s): assign cluster: %s(%s)", lb.Name, lb.Name, lbc.Name, lbc.Id)
		}
	}

	// associate existing lbagents with the cluster
	if len(zoneCluster) > 1 {
		log.Warningf("found %d zones with lb not assigned to any lbcluster, skip assigning lbagent to lbcluster", len(zoneCluster))
		return nil
	}
	for _, lbc := range zoneCluster {
		lbagents := []SLoadbalancerAgent{}
		q := LoadbalancerAgentManager.Query().
			IsNullOrEmpty("cluster_id")
		if err := db.FetchModelObjects(LoadbalancerAgentManager, q, &lbagents); err != nil {
			return errors.WithMessage(err, "find lbagents with empty cluster_id")
		}
		for i := range lbagents {
			lbagent := &lbagents[i]
			if _, err := db.UpdateWithLock(context.Background(), lbagent, func() error {
				lbagent.ClusterId = lbc.Id
				return nil
			}); err != nil {
				return errors.WithMessagef(err, "lbagent %s(%s): assign cluster: %s(%s)",
					lbagent.Name, lbagent.Id, lbc.Name, lbc.Id)
			}
		}
	}

	return nil
}