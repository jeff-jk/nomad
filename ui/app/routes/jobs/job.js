import Ember from 'ember';

const { Route, inject } = Ember;

export default Route.extend({
  store: inject.service(),

  model({ job_id }) {
    return this.get('store')
      .find('job', job_id)
      .then(job => {
        return job.get('allocations').then(() => job);
      });
  },
});
